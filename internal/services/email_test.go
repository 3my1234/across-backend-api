package services

import (
	"encoding/json"
	"errors"
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

func TestEmailOutboxRejectsUnknownTemplate(t *testing.T) {
	service := NewEmailService(config.Config{})
	err := service.SendOutboxTemplate("buyer@example.com", "Buyer", "unknown", json.RawMessage([]byte("{}")), "outbox-id")
	if err == nil || !strings.Contains(err.Error(), "unsupported email template") {
		t.Fatalf("expected unsupported-template error, got %v", err)
	}
}

func TestEmailDeliveryRequiresSMTPConfiguration(t *testing.T) {
	service := NewEmailService(config.Config{})
	payload := json.RawMessage([]byte("{\"reset_url\":\"https://example.com/reset\"}"))
	err := service.SendOutboxTemplate("buyer@example.com", "Buyer", "password_reset", payload, "outbox-id")
	if !errors.Is(err, ErrEmailNotConfigured) {
		t.Fatalf("expected ErrEmailNotConfigured, got %v", err)
	}
}

func TestEmailLayoutFallsBackToBrandNameWithoutLogo(t *testing.T) {
	service := NewEmailService(config.Config{})
	message := service.layout("Welcome", "Preview", "Content")

	if !strings.Contains(message, `>Atlantic Express</div>`) {
		t.Fatal("expected text brand fallback when logo URL is empty")
	}
}
