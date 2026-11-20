package util

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// TOTPSecret returns a new random base32 secret (160 bits, RFC 4226).
func TOTPSecret() string {
	b := make([]byte, 20)
	rand.Read(b)
	return b32.EncodeToString(b)
}

// TOTPURI is the otpauth:// address authenticator apps scan.
func TOTPURI(secret, account, issuer string) string {
	label := url.PathEscape(issuer + ":" + account)
	return fmt.Sprintf("otpauth://totp/%s?secret=%s&issuer=%s&digits=6&period=30", label, secret, url.QueryEscape(issuer))
}

// TOTPCode returns the 6-digit code for a 30-second time step.
func TOTPCode(secret string, step int64) string {
	key, err := b32.DecodeString(strings.ToUpper(strings.ReplaceAll(secret, " ", "")))
	if err != nil {
		return ""
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", v%1000000)
}

// TOTPCheck verifies a code against the current step ±1 and returns the
// matching step (to refuse a code used twice), or -1.
func TOTPCheck(secret, code string, now time.Time) int64 {
	code = strings.TrimSpace(code)
	if len(code) != 6 || secret == "" {
		return -1
	}
	step := now.Unix() / 30
	for _, s := range []int64{step, step - 1, step + 1} {
		if hmac.Equal([]byte(TOTPCode(secret, s)), []byte(code)) {
			return s
		}
	}
	return -1
}
