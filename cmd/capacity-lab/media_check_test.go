package main

import (
	"bytes"
	"image/jpeg"
	"testing"
)

func TestLabObjectOriginFailsClosed(t *testing.T) {
	for _, raw := range []string{"https://api.atlxpres.com/key", "http://" + labMediaHost + "/key", "https://" + labMediaHost + ".evil.test/key", "https://" + labMediaHost + ":444/key", "https://user@" + labMediaHost + "/key", "https://" + labMediaHost + "/key#fragment", "not-url"} {
		if requireLabMediaURL(raw) == nil {
			t.Fatalf("accepted unsafe lab storage URL %q", raw)
		}
	}
	if err := requireLabMediaURL("https://" + labMediaHost + "/private/photo.jpg?X-Amz-Signature=synthetic"); err != nil {
		t.Fatal(err)
	}
}

func TestSyntheticPhotoIsDecodableAndWithinUploadLimit(t *testing.T) {
	previous := 0
	for _, width := range []int{256, 1024, 2048} {
		data, err := syntheticJPEG(width)
		if err != nil {
			t.Fatal(err)
		}
		photo, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if photo.Bounds().Dx() != width || photo.Bounds().Dy() != width || len(data) <= previous || len(data) > 5<<20 {
			t.Fatalf("bad synthetic fixture width=%d bytes=%d", width, len(data))
		}
		previous = len(data)
	}
}
