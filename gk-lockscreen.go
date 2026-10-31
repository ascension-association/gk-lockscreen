// lock screen for gokrazy
// inspired by: https://github.com/gen2brain/goiv/blob/master/goiv_drm.go
package main

import (
	"flag"
	"fmt"
	"image"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/NeowayLabs/drm"
	"github.com/NeowayLabs/drm/mode"
	"github.com/pkg/term"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// exit status that tells the gokrazy supervisor not to restart us
const dontSupervise = 125

type frameBuffer struct {
	id     uint32
	handle uint32
	fb     *mode.FB
	size   uint64
	stride uint32
	screen *os.File
	offset uint64
	data   []byte // only mapped while drawing
	width  int
	height int
	rotate int             // degrees clockwise
	text   image.Rectangle // area covered by the last drawText, in viewer coordinates
}

func main() {
	text := flag.String("text", "LOCKED", "text to display in the center of the screen (empty for a blank screen); if unset, replaced by the device IP address once it has one")
	rotate := flag.Int("rotate", 0, "rotate the text clockwise by 0, 90, 180 or 270 degrees, e.g. for displays mounted in portrait")
	codeFile := flag.String("pairing-code-file", "/run/ramekin/pairing-code", "while this file exists, show the pairing code it holds instead (Ramekin writes it while the device is unclaimed); empty to disable")
	flag.Parse()
	if *rotate%90 != 0 {
		log.Printf("-rotate must be 0, 90, 180 or 270, not %d", *rotate)
		os.Exit(dontSupervise)
	}
	*rotate = (*rotate%360 + 360) % 360
	showIP := true
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "text" {
			showIP = false
		}
	})

	screen, modeset := openDisplay()
	if screen == nil {
		log.Printf("no connected displays, nothing to do")
		os.Exit(dontSupervise)
	}
	var framebufs []frameBuffer
	for _, mod := range modeset.Modesets {
		framebuf, err := createFramebuffer(screen, &mod)
		if err != nil {
			log.Fatal(err)
		}
		framebuf.rotate = *rotate
		if err := framebuf.show(*text); err != nil {
			log.Fatal(err)
		}
		if err := mode.SetCrtc(screen, mod.Crtc, framebuf.id, 0, 0, &mod.Conn, 1, &mod.Mode); err != nil {
			log.Fatalf("SetCrtc: %v", err)
		}
		framebufs = append(framebufs, framebuf)
	}

	// swallow keyboard input so nothing is echoed to the console
	go func() {
		t, err := term.Open("/dev/tty0")
		if err == nil {
			t.SetRaw()
			io.Copy(io.Discard, t)
		}
	}()

	// the network may come up after we start, DHCP may hand out a new
	// address later, and a pairing code may come and go, so keep checking
	// and redraw whenever what should be shown changes
	shown := *text
	for {
		want := shown
		if code := pairingCode(*codeFile); code != "" {
			want = "Pairing code " + code
		} else if !showIP {
			want = *text
		} else if ip := deviceIP(); ip != "" {
			want = ip
		} else if shown != *text && !isIP(shown) {
			want = *text // a pairing code went away before the network came up
		}
		if want != shown {
			for i := range framebufs {
				if err := framebufs[i].show(want); err != nil {
					log.Print(err)
				}
			}
			shown = want
		}
		time.Sleep(5 * time.Second)
	}
}

// pairingCode returns the code in file, if it exists. Ramekin's device
// agent writes it while the device waits to be claimed, so whoever is in
// front of the device can read it off the screen.
func pairingCode(file string) string {
	if file == "" {
		return ""
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	code := strings.TrimSpace(string(b))
	if len(code) > 32 {
		return ""
	}
	return code
}

func isIP(s string) bool { return net.ParseIP(s) != nil }

// deviceIP returns the first non-loopback IPv4 address, falling back to a
// global IPv6 address, or "" if the device has neither yet.
func deviceIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	var v6 string
	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if !ok || !ipnet.IP.IsGlobalUnicast() {
			continue
		}
		if ipnet.IP.To4() != nil {
			return ipnet.IP.String()
		}
		if v6 == "" {
			v6 = ipnet.IP.String()
		}
	}
	return v6
}

// openDisplay returns the first DRM card with a connected display. Boards
// differ in which card drives the display (on a Raspberry Pi 5, card0 is the
// 3D engine and card1 the display controller), so try each in turn.
func openDisplay() (*os.File, *mode.SimpleModeset) {
	cards, _ := filepath.Glob("/dev/dri/card*")
	for _, card := range cards {
		screen, err := os.OpenFile(card, os.O_RDWR, 0)
		if err != nil {
			log.Printf("%s: %v", card, err)
			continue
		}
		if drm.HasDumbBuffer(screen) {
			modeset, err := mode.NewSimpleModeset(screen)
			if err == nil && len(modeset.Modesets) > 0 {
				return screen, modeset
			}
		}
		screen.Close()
	}
	return nil, nil
}

func createFramebuffer(screen *os.File, dev *mode.Modeset) (frameBuffer, error) {
	fb, err := mode.CreateFB(screen, dev.Width, dev.Height, 32)
	if err != nil {
		return frameBuffer{}, fmt.Errorf("CreateFB: %s", err.Error())
	}
	stride := fb.Pitch
	size := fb.Size
	handle := fb.Handle
	fbID, err := mode.AddFB(screen, dev.Width, dev.Height, 24, 32, stride, handle)
	if err != nil {
		return frameBuffer{}, fmt.Errorf("AddFB: %s", err.Error())
	}
	offset, err := mode.MapDumb(screen, handle)
	if err != nil {
		return frameBuffer{}, fmt.Errorf("MapDumb: %s", err.Error())
	}
	// dumb buffers come back zeroed (black), so there is nothing to clear
	framebuf := frameBuffer{
		id:     fbID,
		handle: handle,
		fb:     fb,
		size:   size,
		stride: stride,
		screen: screen,
		offset: offset,
		width:  int(dev.Width),
		height: int(dev.Height),
	}
	return framebuf, nil
}

// show draws s on the framebuffer. Some drivers (e.g. Rockchip) map in
// every page of the buffer at mmap time, which counts the whole buffer
// against our RSS, so it is only mapped for as long as it takes to draw.
func (framebuf *frameBuffer) show(s string) error {
	data, err := syscall.Mmap(int(framebuf.screen.Fd()), int64(framebuf.offset), int(framebuf.size), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return fmt.Errorf("mmap: %s", err.Error())
	}
	framebuf.data = data
	drawText(framebuf, s)
	framebuf.data = nil
	if err := syscall.Munmap(data); err != nil {
		return fmt.Errorf("munmap: %s", err.Error())
	}
	return nil
}

// drawText renders s with the built-in 7x13 bitmap font, scaled up with
// nearest-neighbor so it stays crisp, centered on a black XRGB8888 buffer.
// Any previously drawn text is erased first.
func drawText(framebuf *frameBuffer, s string) {
	// width and height as seen by the viewer, after rotation
	width, height := framebuf.width, framebuf.height
	if framebuf.rotate == 90 || framebuf.rotate == 270 {
		width, height = height, width
	}
	for y := framebuf.text.Min.Y; y < framebuf.text.Max.Y; y++ {
		for x := framebuf.text.Min.X; x < framebuf.text.Max.X; x++ {
			framebuf.setPixel(x, y, 0x00)
		}
	}
	framebuf.text = image.Rectangle{}
	face := basicfont.Face7x13
	textW := font.MeasureString(face, s).Ceil()
	textH := face.Height
	if textW == 0 {
		return
	}
	glyphs := image.NewAlpha(image.Rect(0, 0, textW, textH))
	d := font.Drawer{
		Dst:  glyphs,
		Src:  image.Opaque,
		Face: face,
		Dot:  fixed.P(0, face.Ascent),
	}
	d.DrawString(s)

	// fill up to 80% of the width, but no more than 1/4 of the height
	scale := max(1, min(width*8/10/textW, height/4/textH))
	x0 := (width - textW*scale) / 2
	y0 := (height - textH*scale) / 2
	framebuf.text = image.Rect(x0, y0, x0+textW*scale, y0+textH*scale).Intersect(image.Rect(0, 0, width, height))
	for y := framebuf.text.Min.Y; y < framebuf.text.Max.Y; y++ {
		for x := framebuf.text.Min.X; x < framebuf.text.Max.X; x++ {
			if glyphs.AlphaAt((x-x0)/scale, (y-y0)/scale).A >= 0x80 {
				framebuf.setPixel(x, y, 0xff)
			}
		}
	}
}

// setPixel sets the pixel at viewer coordinates (x, y) to the grey level v,
// mapping them onto the buffer according to the rotation.
func (framebuf *frameBuffer) setPixel(x, y int, v byte) {
	w, h := framebuf.width, framebuf.height
	switch framebuf.rotate {
	case 90:
		x, y = w-1-y, x
	case 180:
		x, y = w-1-x, h-1-y
	case 270:
		x, y = y, h-1-x
	}
	off := y*int(framebuf.stride) + x*4
	copy(framebuf.data[off:off+4], []byte{v, v, v, v}) // B, G, R, X
}
