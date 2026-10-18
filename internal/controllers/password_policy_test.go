package controllers

import (
	"strings"
	"testing"
)

func TestNewPasswordPolicy(t *testing.T) {
	for _, p := range []string{"Password!", "Strong_pass", "Abcdef!1"} {
		if !validNewPassword(p) {
			t.Fatalf("valid password rejected")
		}
	}
	for _, p := range []string{"password!", "PASSWORD!", "Password1", "Ab!", strings.Repeat("A", 73) + "a!"} {
		if validNewPassword(p) {
			t.Fatal("weak/oversize password accepted")
		}
	}
}
