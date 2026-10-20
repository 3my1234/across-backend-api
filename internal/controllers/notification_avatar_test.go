package controllers

import (
	"bytes"
	"github.com/gofiber/fiber/v2"
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"testing"
	"time"
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

func TestCircularNotificationAvatarBoundsFetchAndKeepsPrivateAssetsPrivate(t *testing.T) {
	avatarFetchSlots <- struct{}{}
	avatarFetchSlots <- struct{}{}
	defer func() { <-avatarFetchSlots; <-avatarFetchSlots }()
	controller := &UploadController{}
	app := fiber.New()
	app.Get("/images/*", controller.PublicImageView)
	response, err := app.Test(httptest.NewRequest("GET", "/images/user-uploads/profile/test.png?avatar=1", nil))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 503 || response.Header.Get("Retry-After") != "2" {
		t.Fatal("fetch pressure did not fail fast")
	}
	key := "user-uploads/profile/cached.png"
	private := "private/providers/verification.png"
	avatarCache.Lock()
	avatarCache.items[key] = cachedAvatar{[]byte("cached"), time.Now().Add(time.Minute)}
	avatarCache.items[private] = cachedAvatar{[]byte("private"), time.Now().Add(time.Minute)}
	avatarCache.Unlock()
	defer func() {
		avatarCache.Lock()
		delete(avatarCache.items, key)
		delete(avatarCache.items, private)
		avatarCache.Unlock()
	}()
	response, err = app.Test(httptest.NewRequest("GET", "/images/"+key+"?avatar=1", nil))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("cached avatar should not need a fetch slot")
	}
	response, err = app.Test(httptest.NewRequest("GET", "/images/"+private+"?avatar=1", nil))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatal("cache bypassed private-media protection")
	}
}
