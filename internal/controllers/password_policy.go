package controllers

import "strings"

const passwordRequirements = "Use at least 8 characters, including an uppercase letter, a lowercase letter and a special character (for example !@#$)."

func validNewPassword(password string) bool {
	if len(password) < 8 || len(password) > 72 {
		return false
	}
	upper, lower, special := false, false, false
	for _, c := range password {
		if c >= 'A' && c <= 'Z' {
			upper = true
		}
		if c >= 'a' && c <= 'z' {
			lower = true
		}
		if strings.ContainsRune("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", c) {
			special = true
		}
	}
	return upper && lower && special
}
