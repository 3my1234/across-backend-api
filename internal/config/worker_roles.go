package config

import (
	"errors"
	"strings"
)

// WorkerRoleNames selects existing durable consumers, not new queues or copies
// of business logic. Blank retains the existing all-consumer deployment.
func (c Config) WorkerRoleNames() ([]string, error) {
	all := []string{"web_push", "email", "push", "push_receipts", "auto_confirm", "batch_closure", "settlements", "town_hall_expiry", "driver_expiry"}
	raw := strings.ToLower(strings.TrimSpace(c.WorkerRoles))
	if raw == "" || raw == "all" {
		return all, nil
	}
	allowed := make(map[string]bool, len(all))
	for _, name := range all {
		allowed[name] = true
	}
	seen := make(map[string]bool)
	roles := []string{}
	for _, entry := range strings.Split(raw, ",") {
		name := strings.TrimSpace(entry)
		if !allowed[name] || seen[name] {
			// Do not echo unknown env values into logs.
			return nil, errors.New("WORKER_ROLES must be all or unique supported consumer names")
		}
		seen[name] = true
		roles = append(roles, name)
	}
	return roles, nil
}
