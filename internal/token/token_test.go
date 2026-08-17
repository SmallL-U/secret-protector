package token

import (
	"regexp"
	"testing"
)

func TestGenerateProducesShortURLSafeUniqueTokens(t *testing.T) {
	pattern := regexp.MustCompile(`^sp_[A-Za-z0-9_-]{24}$`)
	seen := make(map[string]struct{}, 100)
	for range 100 {
		value, err := Generate()
		if err != nil {
			t.Fatal(err)
		}
		if !pattern.MatchString(value) {
			t.Fatalf("token has unexpected format: %q", value)
		}
		if _, exists := seen[value]; exists {
			t.Fatal("Generate() returned a duplicate token")
		}
		seen[value] = struct{}{}
	}
}

func TestFingerprintDoesNotExposeToken(t *testing.T) {
	fingerprint := Fingerprint("sp_example-secret")
	if fingerprint == "sp_example-secret" || len(fingerprint) != 8 {
		t.Fatalf("Fingerprint() = %q", fingerprint)
	}
}
