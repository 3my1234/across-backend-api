package middleware

import (
	"net/netip"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/valyala/fasthttp"
)

const verifiedClientIPKey = "atl.verified-client-ip"

// TrustedClientIP requires an operator-verified ingress chain. Only XFF is used;
// CF-Connecting-IP/X-Real-IP supplied by an arbitrary client are never trusted.
func TrustedClientIP(raw string) fiber.Handler {
	var trusted []netip.Prefix
	for _, item := range strings.Split(raw, ",") {
		if prefix, err := netip.ParsePrefix(strings.TrimSpace(item)); err == nil && prefix.Bits() > 0 {
			trusted = append(trusted, prefix)
		}
	}
	return func(c *fiber.Ctx) error {
		peer, _ := netip.AddrFromSlice(c.Context().RemoteIP())
		chain, valid := forwardedForHeader(&c.Request().Header)
		client := peer.Unmap()
		if valid {
			client = resolveClientIP(client, chain, trusted)
		}
		c.Locals(verifiedClientIPKey, client.String())
		return c.Next()
	}
}

// HAProxy may append a separate field rather than extend the first XFF value.
// Preserve every field in wire order; using only the first lets an attacker
// hide the actual untrusted socket peer behind a later, ignored field.
func forwardedForHeader(header *fasthttp.RequestHeader) (string, bool) {
	var chain strings.Builder
	valid := true
	header.VisitAll(func(key, value []byte) {
		if !valid || !strings.EqualFold(string(key), "X-Forwarded-For") {
			return
		}
		if chain.Len()+len(value)+1 > 4096 {
			valid = false
			return
		}
		if chain.Len() > 0 {
			chain.WriteByte(',')
		}
		chain.Write(value)
	})
	return chain.String(), valid
}

func resolveClientIP(peer netip.Addr, header string, trusted []netip.Prefix) netip.Addr {
	isTrusted := func(ip netip.Addr) bool {
		for _, prefix := range trusted {
			if prefix.Contains(ip) {
				return true
			}
		}
		return false
	}
	if !peer.IsValid() || !isTrusted(peer) || len(header) > 4096 {
		return peer
	}
	parts := strings.Split(header, ",")
	if len(parts) > 32 {
		return peer
	}
	chain := make([]netip.Addr, 0, len(parts))
	for _, part := range parts {
		ip, err := netip.ParseAddr(strings.TrimSpace(part))
		if err != nil || ip.Zone() != "" {
			return peer
		}
		chain = append(chain, ip.Unmap())
	}
	current := peer
	for i := len(chain) - 1; i >= 0 && isTrusted(current); i-- {
		current = chain[i]
	}
	return current
}
