package providerlinks

import (
	"html"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestRetiredProviderLinksPreserveNavigationWithoutCredentials(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	target := "https://api.atlxpres.com/open/provider"
	got := RewriteLegacyURL("https://provider.atlxpres.com/?view=messages&conversation="+id+"&token=secret&redirect=https://evil.invalid", target)
	want := target + "?conversation=" + id + "&view=messages"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := RewriteLegacyURL("https://provider.atlxpres.com/?ticket=bad&view=unknown", target); got != target {
		t.Fatal(got)
	}
	if got := RewriteLegacyURL("https://user:secret@provider.atlxpres.com/", target); got != "" {
		t.Fatal("credential-bearing legacy link retained")
	}
	if got := RewriteLegacyURL("https://other.example/path", target); got != "https://other.example/path" {
		t.Fatal("unrelated link changed")
	}
}

func TestAppLandingIsPublicAndDoesNotForwardSecrets(t *testing.T) {
	app := fiber.New()
	app.Get("/open/provider", Landing)
	response, err := app.Test(httptest.NewRequest("GET", "/open/provider?view=subscription&token=secret&redirect=https://evil.invalid", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 || !strings.Contains(html.UnescapeString(string(body)), `href="across://providers?view=subscription"`) {
		t.Fatalf("invalid app landing: %d %s", response.StatusCode, body)
	}
	for _, secret := range []string{"secret", "evil.invalid", "provider.atlxpres.com"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("landing contains %q", secret)
		}
	}
	if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("missing privacy headers")
	}
}
