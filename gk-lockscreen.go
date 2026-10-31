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
	text   image.Rectangle // area covered by the last drawText
}

func main() {
	text := flag.String("text", "LOCKED", "text to display in the center of the screen (empty for a blank screen); if unset, replaced by the device IP address once it has one")
	flag.Parse()
	showIP := true
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "text" {
			showIP = false
		}
	})

	screen, err := drm.OpenCard(0)
	if err != nil {
		log.Printf("no display found, nothing to do: %v", err)
		os.Exit(dontSupervise)
	}
	modeset, err := mode.NewSimpleModeset(screen)
	if err != nil {
		log.Fatalf("NewSimpleModeset: %v", err)
	}
	if len(modeset.Modesets) == 0 {
		log.Printf("no connected displays, nothing to do")
		os.Exit(dontSupervise)
	}
	var framebufs []frameBuffer
	for _, mod := range modeset.Modesets {
		framebuf, err := createFramebuffer(screen, &mod)
		if err != nil {
			log.Fatal(err)
		}
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

	if !showIP {
		select {}
	}
	// the network may come up after we start, and DHCP may hand out a new
	// address later, so keep checking and redraw whenever it changes
	shown := *text
	for {
		if ip := deviceIP(); ip != "" && ip != shown {
			for i := range framebufs {
				if err := framebufs[i].show(ip); err != nil {
					log.Print(err)
				}
			}
			shown = ip
		}
		time.Sleep(5 * time.Second)
	}
}

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
	width, height := framebuf.width, framebuf.height
	for y := framebuf.text.Min.Y; y < framebuf.text.Max.Y; y++ {
		row := framebuf.data[y*int(framebuf.stride):]
		clear(row[framebuf.text.Min.X*4 : framebuf.text.Max.X*4])
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

	// fill up to 80% of the width, but no more than 1/16 of the height: every
	// row we touch costs mapped pages, so keep the text short
	scale := max(1, min(width*8/10/textW, height/16/textH))
	x0 := (width - textW*scale) / 2
	y0 := (height - textH*scale) / 2
	framebuf.text = image.Rect(x0, y0, x0+textW*scale, y0+textH*scale).Intersect(image.Rect(0, 0, width, height))
	for gy := range textH {
		for gx := range textW {
			if glyphs.AlphaAt(gx, gy).A < 0x80 {
				continue
			}
			for y := y0 + gy*scale; y < y0+(gy+1)*scale; y++ {
				if y < 0 || y >= height {
					continue
				}
				row := framebuf.data[y*int(framebuf.stride):]
				for x := x0 + gx*scale; x < x0+(gx+1)*scale; x++ {
					if x < 0 || x >= width {
						continue
					}
					copy(row[x*4:x*4+4], []byte{0xff, 0xff, 0xff, 0xff}) // B, G, R, X
				}
			}
		}
	}
}
