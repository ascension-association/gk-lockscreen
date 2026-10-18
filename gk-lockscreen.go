// lock screen for gokrazy
// inspired by: https://github.com/gen2brain/goiv/blob/master/goiv_drm.go
package main

import (
	"flag"
	"fmt"
	"image"
	"io"
	"log"
	"os"
	"syscall"

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
	data   []byte
}

func main() {
	text := flag.String("text", "LOCKED", "text to display in the center of the screen (empty for a blank screen)")
	flag.Parse()

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
	for _, mod := range modeset.Modesets {
		framebuf, err := createFramebuffer(screen, &mod)
		if err != nil {
			log.Fatal(err)
		}
		if *text != "" {
			drawText(framebuf, int(mod.Width), int(mod.Height), *text)
		}
		if err := mode.SetCrtc(screen, mod.Crtc, framebuf.id, 0, 0, &mod.Conn, 1, &mod.Mode); err != nil {
			log.Fatalf("SetCrtc: %v", err)
		}
	}

	// swallow keyboard input so nothing is echoed to the console
	t, err := term.Open("/dev/tty0")
	if err == nil {
		t.SetRaw()
		io.Copy(io.Discard, t)
	}
	select {}
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
	data, err := syscall.Mmap(int(screen.Fd()), int64(offset), int(size), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return frameBuffer{}, fmt.Errorf("mmap: %s", err.Error())
	}
	clear(data)
	framebuf := frameBuffer{
		id:     fbID,
		handle: handle,
		fb:     fb,
		size:   size,
		stride: stride,
		data:   data,
	}
	return framebuf, nil
}

// drawText renders s with the built-in 7x13 bitmap font, scaled up with
// nearest-neighbor so it stays crisp, centered on a black XRGB8888 buffer.
func drawText(framebuf frameBuffer, width, height int, s string) {
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
