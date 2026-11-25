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
	"sync"
	"sync/atomic"
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

// session is one established channel's crypto state. Several carriers feed
// frames in at once, so the send counter is atomic and the replay window is
// guarded.
type session struct {
	sid     [sidLen]byte
	key     [keyLen]byte
	aead    cipherAEAD
	hpKey   [keyLen]byte
	sendDir byte
	recvDir byte
	sendSeq atomic.Uint64
	recvMu  sync.Mutex
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
	seq := s.sendSeq.Add(1) - 1
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
	s.recvMu.Lock()
	ok := s.replay.ok(seq)
	s.recvMu.Unlock()
	if !ok {
		return nil, errReplay
	}
	return pt, nil
}

// reorderDepth is the largest accepted (top-seq) gap the replay window has seen,
// for tuning the window against real carrier reordering.
func (s *session) reorderDepth() uint64 {
	s.recvMu.Lock()
	defer s.recvMu.Unlock()
	return s.replay.maxReorder
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
	payload = appendHandshakePad(payload) // so the Init size does not fingerprint the handshake
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

// parseInit is the server side: open the Init with the PSK and the static key
// and return the session id, the session key and the client's ephemeral public
// key. It builds neither a Resp nor a session — the caller (Transport.tryInit)
// first decides whether this Init is new and fresh, so a replayed Init can
// never reset a live session (which would reuse record nonces) or draw a Resp.
// Any failure returns errSilent, so the server answers nothing.
func parseInit(psk []byte, static KeyPair, initFrame []byte) (sid [sidLen]byte, key [keyLen]byte, eph [32]byte, err error) {
	if len(initFrame) < ephLen+nonceLn+tagLen {
		err = errSilent
		return
	}
	copy(eph[:], initFrame[:ephLen])
	nonce := initFrame[ephLen : ephLen+nonceLn]
	ct := initFrame[ephLen+nonceLn:]
	k0, e := initKey(psk, eph[:])
	if e != nil {
		err = errSilent
		return
	}
	a0, e := chacha20poly1305.New(k0)
	if e != nil {
		err = errSilent
		return
	}
	payload, e := a0.Open(nil, nonce, ct, eph[:])
	if e != nil || len(payload) < sidLen+tsLen {
		err = errSilent
		return
	}
	ts := time.Unix(int64(binary.BigEndian.Uint64(payload[sidLen:sidLen+tsLen])), 0)
	if d := time.Since(ts); d < -clkSkew || d > clkSkew {
		err = errSilent // stale or future: a replayed handshake
		return
	}
	copy(sid[:], payload[:sidLen])
	dh, e := curve25519.X25519(static.Priv[:], eph[:])
	if e != nil {
		err = errSilent
		return
	}
	key, e = sessionKey(dh, psk, sid)
	if e != nil {
		err = errSilent
	}
	return
}

// makeResp builds the server's Resp, sealed under a key derived separately from
// the record key (so it never shares the record nonce space) and padded so its
// size does not fingerprint the handshake.
func makeResp(key [keyLen]byte, sid [sidLen]byte) ([]byte, error) {
	rk, err := respKey(key)
	if err != nil {
		return nil, err
	}
	ra, err := chacha20poly1305.New(rk[:])
	if err != nil {
		return nil, err
	}
	rn := make([]byte, nonceLn)
	randFill(rn)
	rpayload := make([]byte, tsLen+2)
	binary.BigEndian.PutUint64(rpayload, uint64(time.Now().Unix()))
	rpayload[tsLen], rpayload[tsLen+1] = 'O', 'K'
	rpayload = appendHandshakePad(rpayload)
	rct := ra.Seal(nil, rn, rpayload, sid[:])
	out := make([]byte, 0, nonceLn+len(rct))
	out = append(out, rn...)
	out = append(out, rct...)
	return out, nil
}

// readResp is the client side: confirm the server with the Resp key.
func readResp(sess *session, respFrame []byte) error {
	if len(respFrame) < nonceLn+tagLen {
		return errSilent
	}
	rk, err := respKey(sess.key)
	if err != nil {
		return err
	}
	nonce := respFrame[:nonceLn]
	ct := respFrame[nonceLn:]
	ra, err := chacha20poly1305.New(rk[:])
	if err != nil {
		return err
	}
	payload, err := ra.Open(nil, nonce, ct, sess.sid[:])
	if err != nil || len(payload) < tsLen+2 || payload[tsLen] != 'O' || payload[tsLen+1] != 'K' {
		return errSilent
	}
	return nil
}

func respKey(key [keyLen]byte) ([keyLen]byte, error) {
	var out [keyLen]byte
	k, err := hkdf.Key(sha256.New, key[:], nil, "dvxb/v1/resp", keyLen)
	if err != nil {
		return out, err
	}
	copy(out[:], k)
	return out, nil
}

// appendHandshakePad adds 0..96 random bytes inside the (encrypted) handshake
// payload, so Init and Resp vary in size on the wire. The reader takes only the
// fixed-length head and ignores the rest.
func appendHandshakePad(b []byte) []byte {
	var n [1]byte
	randFill(n[:])
	pad := make([]byte, int(n[0])%97)
	randFill(pad)
	return append(b, pad...)
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
// below the window), so a captured frame cannot be replayed. The window is wide
// because the Bridge stripes frames across carriers with very different RTTs
// (ICMP vs fake-TCP vs CDN), so legitimate frames arrive far out of order at
// high rates; a window too narrow would drop them and force kcp retransmits.
// Not safe for concurrent use: callers hold session.recvMu.
type replayWindow struct {
	top        uint64
	bits       []uint64 // replaySpan bits; bit at distance d (= top-seq) means that seq was seen
	seen       bool
	maxReorder uint64 // the largest accepted (top-seq) gap, for tuning the window
}

const (
	replaySpan  = 4096 // sequence numbers tracked below top
	replayWords = replaySpan / 64
)

func (w *replayWindow) ok(seq uint64) bool {
	if !w.seen {
		w.seen, w.top = true, seq
		w.bits = make([]uint64, replayWords)
		w.set(0)
		return true
	}
	if seq > w.top {
		w.shiftUp(seq - w.top)
		w.top = seq
		w.set(0)
		return true
	}
	back := w.top - seq
	if back >= replaySpan {
		return false // too old to prove it is not a replay
	}
	if back > w.maxReorder {
		w.maxReorder = back
	}
	if w.get(back) {
		return false
	}
	w.set(back)
	return true
}

func (w *replayWindow) set(d uint64)      { w.bits[d/64] |= 1 << (d % 64) }
func (w *replayWindow) get(d uint64) bool { return w.bits[d/64]&(1<<(d%64)) != 0 }

// shiftUp moves every recorded distance up by shift bits (the window slid
// forward), dropping anything that falls past replaySpan.
func (w *replayWindow) shiftUp(shift uint64) {
	if shift >= replaySpan {
		for i := range w.bits {
			w.bits[i] = 0
		}
		return
	}
	words := int(shift / 64)
	b := uint(shift % 64)
	for i := replayWords - 1; i >= 0; i-- {
		var lo, hi uint64
		if i-words >= 0 {
			lo = w.bits[i-words]
		}
		if b > 0 && i-words-1 >= 0 {
			hi = w.bits[i-words-1]
		}
		if b == 0 {
			w.bits[i] = lo
		} else {
			w.bits[i] = (lo << b) | (hi >> (64 - b))
		}
	}
}
