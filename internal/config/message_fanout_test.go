package config

import "testing"

func TestAsyncMessageNotificationsRequiresExplicitOptIn(t *testing.T) {
	for _, value := range []string{"", "false", "true"} {
		t.Setenv("ASYNC_MESSAGE_NOTIFICATIONS", value)
		if got := Load().AsyncMessageNotifications; got != (value == "true") {
			t.Fatal("unexpected notification rollout mode", value, got)
		}
	}
	roles, err := (Config{WorkerRoles: "message_fanout"}).WorkerRoleNames()
	if err != nil || len(roles) != 1 || roles[0] != "message_fanout" {
		t.Fatal("dedicated durable fanout role unavailable", roles, err)
	}
}
