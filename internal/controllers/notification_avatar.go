package controllers

import (
	"bytes"
	"image"
	"image/png"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	_ "golang.org/x/image/webp"
)

var avatarSlots = make(chan struct{}, 2)
var avatarCache = struct {
	sync.Mutex
	items map[string]cachedAvatar
}{items: map[string]cachedAvatar{}}

type cachedAvatar struct {
	data  []byte
	until time.Time
}

func cachedNotificationAvatar(key string) []byte {
	avatarCache.Lock()
	defer avatarCache.Unlock()
	if item, ok := avatarCache.items[key]; ok {
		if time.Now().Before(item.until) {
			return item.data
		}
		delete(avatarCache.items, key)
	}
	return nil
}
func circularNotificationAvatar(data []byte) ([]byte, error) {
	select {
	case avatarSlots <- struct{}{}:
		defer func() { <-avatarSlots }()
	default:
		return nil, fiber.NewError(503, "Avatar processing is busy")
	}
	meta, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || meta.Width <= 0 || meta.Height <= 0 || int64(meta.Width)*int64(meta.Height) > 16<<20 {
		return nil, fiber.NewError(502, "Avatar format or dimensions unavailable")
	}
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fiber.NewError(502, "Avatar unavailable")
	}
	const size = 128
	result := image.NewNRGBA(image.Rect(0, 0, size, size))
	bounds := source.Bounds()
	side := bounds.Dx()
	if bounds.Dy() < side {
		side = bounds.Dy()
	}
	left, top := bounds.Min.X+(bounds.Dx()-side)/2, bounds.Min.Y+(bounds.Dy()-side)/2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := float64(x)+0.5-size/2, float64(y)+0.5-size/2
			if dx*dx+dy*dy <= (size/2)*(size/2) {
				result.Set(x, y, source.At(left+x*side/size, top+y*side/size))
			}
		}
	}
	var encoded bytes.Buffer
	if err = png.Encode(&encoded, result); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}
func sendNotificationAvatar(c *fiber.Ctx, key string, data []byte) error {
	avatarCache.Lock()
	if len(avatarCache.items) >= 128 {
		for old := range avatarCache.items {
			delete(avatarCache.items, old)
			break
		}
	}
	avatarCache.items[key] = cachedAvatar{data, time.Now().Add(time.Hour)}
	avatarCache.Unlock()
	c.Set("Cache-Control", "public, max-age=3600")
	c.Type("png")
	return c.Send(data)
}
