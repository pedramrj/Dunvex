package util

import (
	"testing"
	"time"
)

// RFC 6238 appendix B vectors (SHA-1, 8 digits) truncated to 6 digits.
func TestTOTPVectors(t *testing.T) {
	secret := b32.EncodeToString([]byte("12345678901234567890"))
	for at, want := range map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"} {
		if got := TOTPCode(secret, at/30); got != want {
			t.Errorf("t=%d: got %s want %s", at, got, want)
		}
	}
	now := time.Unix(1234567890, 0)
	if TOTPCheck(secret, "005924", now) < 0 {
		t.Error("current code refused")
	}
	if TOTPCheck(secret, "000000", now) >= 0 {
		t.Error("wrong code accepted")
	}
}
