// lock screen for gokrazy
// inspired by: https://github.com/gen2brain/goiv/blob/master/goiv_drm.go
package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/NeowayLabs/drm"
	"github.com/NeowayLabs/drm/mode"
	"github.com/pkg/term"
)

type frameBuffer struct {
	id     uint32
	handle uint32
	fb     *mode.FB
	size   uint64
	stride uint32
}

type msetData struct {
	mode      *mode.Modeset
	fb        frameBuffer
	savedCrtc *mode.Crtc
}

func main() {
	screen, _ := drm.OpenCard(0)
	modeset, _ := mode.NewSimpleModeset(screen)
	var msets []msetData
	for _, mod := range modeset.Modesets {
		framebuf, _ := createFramebuffer(screen, &mod)
		savedCrtc, _ := mode.GetCrtc(screen, mod.Crtc)
		mode.SetCrtc(screen, mod.Crtc, framebuf.id, 0, 0, &mod.Conn, 1, &mod.Mode)
		msets = append(msets, msetData{
			mode:      &mod,
			fb:        framebuf,
			savedCrtc: savedCrtc,
		})
	}
	t, _ := term.Open("/dev/tty0")
	t.SetRaw()
	char := func() ([]byte, error) {
		bytes := make([]byte, 3)
		n, err := t.Read(bytes)
		if err != nil {
			return nil, err
		}
		return bytes[0:n], nil
	}
	for {
		c, _ := char()
		switch {
		case bytes.Equal(c, []byte{3}), bytes.Equal(c, []byte{113}), bytes.Equal(c, []byte{27}): // ctrl+c, q, Esc
			//os.Exit(0)
		}
	}
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
	framebuf := frameBuffer{
		id:     fbID,
		handle: handle,
		fb:     fb,
		size:   size,
		stride: stride,
	}
	return framebuf, nil
}
