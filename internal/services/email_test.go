package services

import (
	"strings"
	"testing"

	"across/backend/internal/config"
)

func TestEmailLayoutUsesBrandingAndEscapesMetadata(t *testing.T) {
	service := NewEmailService(config.Config{
		BrandLogoURL: "https://media.example/logo.png?size=large&format=png",
	})

	message := service.layout(`<Welcome>`, `Preview & details`, `<p>Body</p>`)

	for _, expected := range []string{
		`alt="Atlantic Express"`,
		`From China to Africa, delivering possibilities`,
		`ATLANTIC SHANSU LOGISTICS LIMITED`,
		`https://media.example/logo.png?size=large&amp;format=png`,
		`&lt;Welcome&gt;`,
		`Preview &amp; details`,
		`<p>Body</p>`,
	} {
		if !strings.Contains(message, expected) {
			t.Fatalf("expected branded email to contain %q", expected)
		}
	}
}

func TestEmailLayoutFallsBackToBrandNameWithoutLogo(t *testing.T) {
	service := NewEmailService(config.Config{})
	message := service.layout("Welcome", "Preview", "Content")

	if !strings.Contains(message, `>Atlantic Express</div>`) {
		t.Fatal("expected text brand fallback when logo URL is empty")
	}
}
