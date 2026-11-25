package bridge

// The session layer: an authenticated, forward-secret, replay-protected,
// probe-resistant channel over whatever frames the carriers move.
//
// Handshake. The client (abroad) makes an ephemeral X25519 key, the server
// (the Iran relay) holds a static X25519 key the client has pinned, and both
// share a pre-shared key (the relay's secret). The Init frame is sealed with a
// key derived from the PSK alone, so a prober without the PSK cannot forge one
// and the server answers nothing at all (no error, no RST, no packet) — the
// Bridge is silent to anyone without the key. The session key then mixes the
// PSK with X25519(ephemeral, static), so it is authenticated by the PSK and
// forward-secret against a later PSK leak through the ephemeral. (A static-key
// compromise is not covered; that would need a server ephemeral and a second
// round trip, i.e. full Noise IK — a later hardening.)
//
// Records. Each frame is ChaCha20-Poly1305 with a per-direction counter nonce,
// so a nonce never repeats under one session key, and the 16-byte sid+seq
// header is masked QUIC-style from the ciphertext, so frames look like random
// bytes end to end. A sliding window drops replays; a different session has a
// different key, so a frame can never be replayed into another session.

import (
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"time"

	"golang.org/x/crypto/chacha20"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
)

const (
	keyLen  = 32
	sidLen  = 8
	seqLen  = 8
	hdrLen  = sidLen + seqLen // 16; also the header-protection sample size
	tagLen  = 16
	nonceLn = chacha20poly1305.NonceSize // 12
	ephLen  = 32
	tsLen   = 8
	clkSkew = 120 * time.Second // a handshake older/newer than this is refused
)

// directions keep the two nonce spaces apart, so one key is safe both ways.
const (
	dirC2S = 0x01 // client (abroad) to server (Iran relay)
	dirS2C = 0x02
)

// KeyPair is a static X25519 key pair (the relay's own, pinned by the target).
type KeyPair struct {
	Priv [32]byte
	Pub  [32]byte
}

// GenerateKey makes a static key pair.
func GenerateKey() (KeyPair, error) {
	var kp KeyPair
	if _, err := rand.Read(kp.Priv[:]); err != nil {
		return kp, err
	}
	pub, err := curve25519.X25519(kp.Priv[:], curve25519.Basepoint)
	if err != nil {
		return kp, err
	}
	copy(kp.Pub[:], pub)
	return kp, nil
}

// PubFromPriv recomputes a public key from a private one.
func PubFromPriv(priv [32]byte) ([32]byte, error) {
	var pub [32]byte
	p, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		return pub, err
	}
	copy(pub[:], p)
	return pub, nil
}

// session is one established channel's crypto state.
type session struct {
	sid     [sidLen]byte
	key     [keyLen]byte
	aead    cipherAEAD
	hpKey   [keyLen]byte
	sendDir byte
	recvDir byte
	sendSeq uint64
	replay  replayWindow
}

type cipherAEAD interface {
	Seal(dst, nonce, plaintext, aad []byte) []byte
	Open(dst, nonce, ciphertext, aad []byte) ([]byte, error)
}

func newSession(sid [sidLen]byte, key [keyLen]byte, client bool) (*session, error) {
	aead, err := chacha20poly1305.New(key[:])
	if err != nil {
		return nil, err
	}
	s := &session{sid: sid, key: key, aead: aead}
	hp, err := hkdf.Key(sha256.New, key[:], nil, "dvxb/v1/hp", keyLen)
	if err != nil {
		return nil, err
	}
	copy(s.hpKey[:], hp)
	if client {
		s.sendDir, s.recvDir = dirC2S, dirS2C
	} else {
		s.sendDir, s.recvDir = dirS2C, dirC2S
	}
	return s, nil
}

// protect seals one plaintext frame for the wire.
func (s *session) protect(plaintext []byte) []byte {
	seq := s.sendSeq
	s.sendSeq++
	hdr := make([]byte, hdrLen)
	copy(hdr, s.sid[:])
	binary.BigEndian.PutUint64(hdr[sidLen:], seq)
	nonce := make([]byte, nonceLn)
	nonce[0] = s.sendDir
	binary.BigEndian.PutUint64(nonce[nonceLn-8:], seq)
	ct := s.aead.Seal(nil, nonce, plaintext, hdr) // hdr (unmasked) is the AAD
	frame := make([]byte, hdrLen+len(ct))
	copy(frame, hdr)
	copy(frame[hdrLen:], ct)
	hpApply(frame[:hdrLen], ct, s.hpKey[:])
	return frame
}

// unprotect opens one wire frame, checking the session id and replay. It
// returns errSilent for anything it cannot open, so the caller drops it with
// no reply.
func (s *session) unprotect(frame []byte) ([]byte, error) {
	if len(frame) < hdrLen+hdrLen { // need >=16 bytes of ciphertext for the HP sample
		return nil, errSilent
	}
	hdr := make([]byte, hdrLen)
	copy(hdr, frame[:hdrLen])
	ct := frame[hdrLen:]
	hpApply(hdr, ct, s.hpKey[:]) // unmask (XOR is its own inverse)
	if subtle.ConstantTimeCompare(hdr[:sidLen], s.sid[:]) != 1 {
		return nil, errSilent
	}
	seq := binary.BigEndian.Uint64(hdr[sidLen:])
	nonce := make([]byte, nonceLn)
	nonce[0] = s.recvDir
	binary.BigEndian.PutUint64(nonce[nonceLn-8:], seq)
	pt, err := s.aead.Open(nil, nonce, ct, hdr)
	if err != nil {
		return nil, errSilent
	}
	if !s.replay.ok(seq) {
		return nil, errReplay
	}
	return pt, nil
}

var (
	errSilent = errors.New("bridge: frame not for this session") // never answered
	errReplay = errors.New("bridge: replayed frame")
)

// hpApply masks or unmasks the header from a ciphertext sample (QUIC header
// protection). XOR is its own inverse, so one function does both.
func hpApply(hdr, ct, hpKey []byte) {
	if len(ct) < 16 {
		return
	}
	sample := ct[:16]
	var nonce [12]byte
	copy(nonce[:], sample[4:16])
	ctr := binary.LittleEndian.Uint32(sample[0:4])
	c, err := chacha20.NewUnauthenticatedCipher(hpKey, nonce[:])
	if err != nil {
		return
	}
	c.SetCounter(ctr)
	var ks [hdrLen]byte
	c.XORKeyStream(ks[:], ks[:])
	for i := 0; i < hdrLen && i < len(hdr); i++ {
		hdr[i] ^= ks[i]
	}
}

// ---------------------------------------------------------------- handshake

// Init is the client's opening frame: the ephemeral public key, then a payload
// sealed under a key derived from the PSK alone.
func makeInit(psk []byte, serverStatic [32]byte) (initFrame []byte, sess *session, err error) {
	var ephPriv [32]byte
	if _, err = rand.Read(ephPriv[:]); err != nil {
		return nil, nil, err
	}
	ephPub, err := PubFromPriv(ephPriv)
	if err != nil {
		return nil, nil, err
	}
	var sid [sidLen]byte
	if _, err = rand.Read(sid[:]); err != nil {
		return nil, nil, err
	}
	k0, err := initKey(psk, ephPub[:])
	if err != nil {
		return nil, nil, err
	}
	a0, err := chacha20poly1305.New(k0)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, nonceLn)
	randFill(nonce)
	payload := make([]byte, sidLen+tsLen)
	copy(payload, sid[:])
	binary.BigEndian.PutUint64(payload[sidLen:], uint64(time.Now().Unix()))
	ct := a0.Seal(nil, nonce, payload, ephPub[:])
	initFrame = make([]byte, 0, ephLen+nonceLn+len(ct))
	initFrame = append(initFrame, ephPub[:]...)
	initFrame = append(initFrame, nonce...)
	initFrame = append(initFrame, ct...)

	dh, err := curve25519.X25519(ephPriv[:], serverStatic[:])
	if err != nil {
		return nil, nil, err
	}
	key, err := sessionKey(dh, psk, sid)
	if err != nil {
		return nil, nil, err
	}
	sess, err = newSession(sid, key, true)
	if err != nil {
		return nil, nil, err
	}
	return initFrame, sess, nil
}

// readInit is the server side: open the Init with the PSK and the static key.
// Any failure returns errSilent, so the server answers nothing.
func readInit(psk []byte, static KeyPair, initFrame []byte) (respFrame []byte, sess *session, err error) {
	if len(initFrame) < ephLen+nonceLn+tagLen {
		return nil, nil, errSilent
	}
	var ephPub [32]byte
	copy(ephPub[:], initFrame[:ephLen])
	nonce := initFrame[ephLen : ephLen+nonceLn]
	ct := initFrame[ephLen+nonceLn:]
	k0, err := initKey(psk, ephPub[:])
	if err != nil {
		return nil, nil, errSilent
	}
	a0, err := chacha20poly1305.New(k0)
	if err != nil {
		return nil, nil, errSilent
	}
	payload, err := a0.Open(nil, nonce, ct, ephPub[:])
	if err != nil || len(payload) < sidLen+tsLen {
		return nil, nil, errSilent
	}
	ts := time.Unix(int64(binary.BigEndian.Uint64(payload[sidLen:])), 0)
	if d := time.Since(ts); d < -clkSkew || d > clkSkew {
		return nil, nil, errSilent // stale or future: a replayed handshake
	}
	var sid [sidLen]byte
	copy(sid[:], payload[:sidLen])
	dh, err := curve25519.X25519(static.Priv[:], ephPub[:])
	if err != nil {
		return nil, nil, errSilent
	}
	key, err := sessionKey(dh, psk, sid)
	if err != nil {
		return nil, nil, errSilent
	}
	sess, err = newSession(sid, key, false)
	if err != nil {
		return nil, nil, errSilent
	}
	// Resp, sealed under the session key: proves the server holds the static key.
	rn := make([]byte, nonceLn)
	randFill(rn)
	rpayload := make([]byte, tsLen+2)
	binary.BigEndian.PutUint64(rpayload, uint64(time.Now().Unix()))
	rpayload[tsLen], rpayload[tsLen+1] = 'O', 'K'
	ra, _ := chacha20poly1305.New(key[:])
	rct := ra.Seal(nil, rn, rpayload, sid[:])
	respFrame = make([]byte, 0, nonceLn+len(rct))
	respFrame = append(respFrame, rn...)
	respFrame = append(respFrame, rct...)
	return respFrame, sess, nil
}

// readResp is the client side: confirm the server with the session key.
func readResp(sess *session, respFrame []byte) error {
	if len(respFrame) < nonceLn+tagLen {
		return errSilent
	}
	nonce := respFrame[:nonceLn]
	ct := respFrame[nonceLn:]
	ra, err := chacha20poly1305.New(sess.key[:])
	if err != nil {
		return err
	}
	payload, err := ra.Open(nil, nonce, ct, sess.sid[:])
	if err != nil || len(payload) < tsLen+2 || payload[tsLen] != 'O' || payload[tsLen+1] != 'K' {
		return errSilent
	}
	return nil
}

func initKey(psk, ephPub []byte) ([]byte, error) {
	return hkdf.Key(sha256.New, psk, ephPub, "dvxb/v1/init", keyLen)
}

func sessionKey(dh, psk []byte, sid [sidLen]byte) ([keyLen]byte, error) {
	var out [keyLen]byte
	ikm := make([]byte, 0, len(dh)+len(psk))
	ikm = append(ikm, dh...)
	ikm = append(ikm, psk...)
	k, err := hkdf.Key(sha256.New, ikm, sid[:], "dvxb/v1/session", keyLen)
	if err != nil {
		return out, err
	}
	copy(out[:], k)
	return out, nil
}

// ---------------------------------------------------------------- replay window

// replayWindow drops frames whose sequence number was already seen (or is far
// below the window), so a captured frame cannot be replayed.
type replayWindow struct {
	top  uint64
	bits uint64 // the 64 sequence numbers below top
	seen bool
}

const replaySpan = 64

func (w *replayWindow) ok(seq uint64) bool {
	if !w.seen {
		w.seen, w.top, w.bits = true, seq, 1
		return true
	}
	if seq > w.top {
		shift := seq - w.top
		if shift >= replaySpan {
			w.bits = 1
		} else {
			w.bits = (w.bits << shift) | 1
		}
		w.top = seq
		return true
	}
	back := w.top - seq
	if back >= replaySpan {
		return false // too old to prove it is not a replay
	}
	mask := uint64(1) << back
	if w.bits&mask != 0 {
		return false
	}
	w.bits |= mask
	return true
}
