package auth

import "strings"

// CanonicalAdminRole never upgrades an unknown role to a valid role.
func CanonicalAdminRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "admin", "admin_i", "admin_ii", "admin_iii", "procurement_admin", "courier_admin", "catalog_admin":
		return "catalog_admin"
	case "super_admin":
		return "super_admin"
	case "support_admin":
		return "support_admin"
	case "moderation_admin":
		return "moderation_admin"
	case "finance_admin":
		return "finance_admin"
	case "ads_admin":
		return "ads_admin"
	default:
		return ""
	}
}
