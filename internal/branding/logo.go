package branding

import (
	"bytes"
	_ "embed"
	"golang.org/x/image/draw"
	"image"
	"image/png"
)

// Logo is shared by the public brand endpoint and inline transactional emails.
//
//go:embed logo.png
var Logo []byte

// Email uses a small 2x-density copy instead of attaching the full public asset.
var EmailLogo = emailLogo()

func emailLogo() []byte {
	source, err := png.Decode(bytes.NewReader(Logo))
	if err != nil {
		return Logo
	}
	bounds := source.Bounds()
	if bounds.Dx() <= 380 {
		return Logo
	}
	height := bounds.Dy() * 380 / bounds.Dx()
	if height < 1 {
		height = 1
	}
	result := image.NewNRGBA(image.Rect(0, 0, 380, height))
	draw.CatmullRom.Scale(result, result.Bounds(), source, bounds, draw.Src, nil)
	var encoded bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	if encoder.Encode(&encoded, result) != nil {
		return Logo
	}
	return encoded.Bytes()
}

const EmailLogoCID = "atlantic-express-logo@atlxpres.com"
