package middleware

import (
	"golang.org/x/crypto/bcrypt"
	"testing"
)

func BenchmarkBcryptCompareCost10(b *testing.B) {
	hash, err := bcrypt.GenerateFromPassword([]byte("local-synthetic-password"), 10)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := bcrypt.CompareHashAndPassword(hash, []byte("local-synthetic-password")); err != nil {
			b.Fatal(err)
		}
	}
}
