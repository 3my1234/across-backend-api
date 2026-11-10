// Package providerlinks preserves old provider links without hosting a web workspace.
package providerlinks

import (
	"html"
	"net/url"
	"regexp"
	"strings"

	"github.com/gofiber/fiber/v2"
)

var uuid = regexp.MustCompile(`(?i)^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func navigation(query url.Values) url.Values {
	result := url.Values{}
	view := query.Get("view")
	switch view {
	case "overview", "profile", "verification", "subscription", "products", "listings", "requests", "messages", "support", "merchant-orders", "more":
		result.Set("view", view)
	}
	for _, key := range []string{"conversation", "shared", "hall", "ticket", "event", "notification", "listing", "request"} {
		if value := query.Get(key); uuid.MatchString(value) {
			result.Set(key, value)
		}
	}
	return result
}

// RewriteLegacyURL also covers queued email payloads, without changing stored jobs.
func RewriteLegacyURL(raw, appURL string) string {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Hostname(), "provider.atlxpres.com") {
		return raw
	}
	if u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	if query := navigation(u.Query()).Encode(); query != "" {
		return appURL + "?" + query
	}
	return appURL
}

// Landing carries navigation only. The app still authenticates and authorizes the account.
func Landing(c *fiber.Ctx) error {
	query, err := url.ParseQuery(string(c.Request().URI().QueryString()))
	if err != nil {
		query = url.Values{}
	}
	link := "across://providers"
	if safe := navigation(query).Encode(); safe != "" {
		link += "?" + safe
	}
	c.Set("Cache-Control", "no-store")
	c.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'")
	c.Set("Referrer-Policy", "no-referrer")
	c.Type("html", "utf-8")
	return c.SendString(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Open ATL Xpress</title><style>body{font:18px system-ui;max-width:34rem;margin:12vh auto;padding:24px;line-height:1.6;color:#14352b}a{color:#096445}.open{display:inline-block;background:#096445;color:white;padding:12px 20px;border-radius:8px}</style><h1>Your provider workspace is in the ATL Xpress app</h1><p>Use the same account to manage your profile, verification, listings, subscriptions, bookings and messages.</p><p><a class="open" href="` + html.EscapeString(link) + `">Open ATL Xpress</a></p><p>If the app does not open, install or update ATL Xpress on your phone, sign in, and choose Provider workspace in your account.</p><p><a href="https://atlxpres.com">ATL Xpress website</a></p></html>`)
}
