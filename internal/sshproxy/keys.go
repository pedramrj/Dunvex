package sshproxy

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"

	"golang.org/x/crypto/ssh"
)

// The host key is an Ed25519 key, made once and kept as its 32-byte seed
// (base64) in the inbound, so the server keeps its identity across restarts
// and apps that pin it keep trusting it.

// NewHostKey makes a new host key and returns its seed.
func NewHostKey() string {
	seed := make([]byte, ed25519.SeedSize)
	rand.Read(seed)
	return base64.StdEncoding.EncodeToString(seed)
}

func seedKey(seed string) (ed25519.PrivateKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(seed))
	if err != nil || len(b) != ed25519.SeedSize {
		return nil, errors.New("ssh: the host key is not valid")
	}
	return ed25519.NewKeyFromSeed(b), nil
}

// HostKey is the server's signer for a kept seed.
func HostKey(seed string) (ssh.Signer, error) {
	k, err := seedKey(seed)
	if err != nil {
		return nil, err
	}
	return ssh.NewSignerFromKey(k)
}

// PublicKey is the host key as apps pin it, "ssh-ed25519 AAAA…" ("" for a bad seed).
func PublicKey(seed string) string {
	s, err := HostKey(seed)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(s.PublicKey())))
}

// Fingerprint is the host key's SHA256 fingerprint, as SSH apps show it
// ("SHA256:…"; "" for a bad seed).
func Fingerprint(seed string) string {
	s, err := HostKey(seed)
	if err != nil {
		return ""
	}
	return ssh.FingerprintSHA256(s.PublicKey())
}
