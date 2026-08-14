package controllers

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"github.com/gen2brain/avif"
)

func TestTranscodeAVIFToJPEG(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 2, 2))
	source.Set(0, 0, color.RGBA{R: 255, A: 255})
	source.Set(1, 0, color.RGBA{G: 255, A: 255})
	source.Set(0, 1, color.RGBA{B: 255, A: 255})
	source.Set(1, 1, color.RGBA{R: 255, G: 255, A: 255})

	var encoded bytes.Buffer
	if err := avif.Encode(&encoded, source); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}

	converted, err := transcodeAVIFToJPEG(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatalf("transcode: %v", err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(converted))
	if err != nil {
		t.Fatalf("decode JPEG: %v", err)
	}
	if decoded.Bounds().Dx() != 2 || decoded.Bounds().Dy() != 2 {
		t.Fatalf("unexpected output bounds: %v", decoded.Bounds())
	}
}

func TestTranscodeAVIFRejectsOversizedInput(t *testing.T) {
	_, err := transcodeAVIFToJPEG(bytes.NewReader(make([]byte, maxImageProxyBytes+1)))
	if err == nil {
		t.Fatal("expected oversized input to be rejected")
	}
}
