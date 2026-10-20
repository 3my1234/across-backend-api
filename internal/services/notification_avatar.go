package services

import (
	"net/url"
	"strings"
)

// Notifications use only public uploaded images, never private/signed links or
// arbitrary remote fetch destinations. The existing API owns the bucket lookup.
func notificationAvatar(raw string) string {
	key := strings.TrimSpace(raw)
	if strings.HasPrefix(key, "https://") {
		u, err := url.Parse(key)
		if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Port() != "" {
			return ""
		}
		host := strings.ToLower(u.Hostname())
		if host != "api.atlxpres.com" && host != "atlxpres.com" && !strings.HasSuffix(host, ".atlxpres.com") && !(strings.Contains(host, ".s3.") && strings.HasSuffix(host, ".amazonaws.com")) {
			return ""
		}
		key = strings.TrimPrefix(u.Path, "/api/v1/public/images/view/")
		key = strings.TrimLeft(key, "/")
	}
	if !strings.HasPrefix(key, "user-uploads/") || strings.Contains(key, "..") || strings.Contains(key, "private") || strings.Contains(key, "/verification/") || strings.Contains(key, "?") || strings.Contains(key, "#") {
		return ""
	}
	return "https://api.atlxpres.com/api/v1/public/images/view/" + key + "?avatar=1"
}
