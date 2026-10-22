package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestWorkerRoleSelection(t *testing.T) {
	for _, value := range []string{"", "all", " ALL "} {
		roles, err := (Config{WorkerRoles: value}).WorkerRoleNames()
		if err != nil || len(roles) != 9 {
			t.Fatal("default must retain all existing consumers")
		}
	}
	roles, err := (Config{WorkerRoles: " PUSH, push_receipts,web_push "}).WorkerRoleNames()
	if err != nil || !reflect.DeepEqual(roles, []string{"push", "push_receipts", "web_push"}) {
		t.Fatal("selection changed")
	}
	for _, value := range []string{"all,email", "email,email", "email,", "private-secret-value", ","} {
		_, err := (Config{WorkerRoles: value}).WorkerRoleNames()
		if err == nil || strings.Contains(err.Error(), "private-secret-value") {
			t.Fatal("invalid selection must fail without echoing env values")
		}
		if (Config{AppEnv: "test", WorkerRoles: value}).Validate("worker") == nil {
			t.Fatal("worker must reject before dependencies in every environment")
		}
	}
}
