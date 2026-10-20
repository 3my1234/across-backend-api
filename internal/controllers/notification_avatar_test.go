package controllers

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestCircularNotificationAvatarHasTransparentCorners(t *testing.T) {
	photo := image.NewNRGBA(image.Rect(0, 0, 320, 160))
	for y := 0; y < 160; y++ {
		for x := 0; x < 320; x++ {
			photo.Set(x, y, color.NRGBA{R: 200, G: 50, B: 20, A: 255})
		}
	}
	var input bytes.Buffer
	if err := png.Encode(&input, photo); err != nil {
		t.Fatal(err)
	}
	data, err := circularNotificationAvatar(input.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	result, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if result.Bounds().Dx() != 128 || result.Bounds().Dy() != 128 {
		t.Fatal("avatar not bounded")
	}
	_, _, _, corner := result.At(0, 0).RGBA()
	_, _, _, center := result.At(64, 64).RGBA()
	if corner != 0 || center == 0 {
		t.Fatal("avatar not circular")
	}
	if _, err = circularNotificationAvatar([]byte("not an image")); err == nil {
		t.Fatal("invalid image accepted")
	}
}
