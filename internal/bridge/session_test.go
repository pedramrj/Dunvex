package bridge

import (
	"bytes"
	"testing"
)

// A full handshake, then a data frame each way, and a prober with the wrong
// key gets silence.
func TestHandshakeAndFrames(t *testing.T) {
	psk := []byte("a-shared-secret-of-enough-length")
	srv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	initFrame, cs, err := makeInit(psk, srv.Pub)
	if err != nil {
		t.Fatal(err)
	}
	sid, key, _, err := parseInit(psk, srv, initFrame)
	if err != nil {
		t.Fatalf("server refused a valid init: %v", err)
	}
	ss, err := newSession(sid, key, false)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := makeResp(key, sid)
	if err != nil {
		t.Fatal(err)
	}
	if err := readResp(cs, resp); err != nil {
		t.Fatalf("client refused a valid resp: %v", err)
	}
	if cs.sid != ss.sid {
		t.Fatal("session ids differ")
	}

	// client -> server
	msg := []byte("hello across the border, this is a kcp-sized packet payload")
	frame := cs.protect(msg)
	got, err := ss.unprotect(frame)
	if err != nil || !bytes.Equal(got, msg) {
		t.Fatalf("c2s: %v %q", err, got)
	}
	// server -> client
	msg2 := []byte("and the answer comes back the easy way")
	frame2 := ss.protect(msg2)
	got2, err := cs.unprotect(frame2)
	if err != nil || !bytes.Equal(got2, msg2) {
		t.Fatalf("s2c: %v %q", err, got2)
	}
	// the header must not look constant on the wire: two frames of the same
	// plaintext differ (counter nonce + header protection).
	if bytes.Equal(cs.protect(msg)[:hdrLen], cs.protect(msg)[:hdrLen]) {
		t.Fatal("frame headers are identical; header protection is not working")
	}
}

func TestProbeResistance(t *testing.T) {
	srv, _ := GenerateKey()
	right := []byte("the-right-pre-shared-key-value!!")
	// A prober with the wrong PSK: every init is refused with silence.
	initFrame, _, err := makeInit([]byte("the-wrong-pre-shared-key-value!!"), srv.Pub)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := parseInit(right, srv, initFrame); err != errSilent {
		t.Fatalf("want errSilent for a wrong-key init, got %v", err)
	}
	// Random junk is also silent, never an answer.
	junk := make([]byte, 200)
	randFill(junk)
	if _, _, _, err := parseInit(right, srv, junk); err != errSilent {
		t.Fatalf("want errSilent for junk, got %v", err)
	}
}

// A replayed Init, after the session is up, must not reset the session or its
// send counter, and must draw no Resp from another carrier or after the window.
func TestReplayedInitDoesNotReset(t *testing.T) {
	psk := []byte("replay-init-test-shared-key-00001")
	srv, _ := GenerateKey()
	// one server-side Transport with two stub carriers (so we can replay "from
	// another carrier")
	c0, c1 := &countingCarrier{}, &countingCarrier{}
	tr := newTransport(roleServer, psk, srv, [32]byte{}, []*carrierState{{c: c0}, {c: c1}})

	initFrame, _, err := makeInit(psk, srv.Pub)
	if err != nil {
		t.Fatal(err)
	}
	// first Init on carrier 0 from source "A": installs the session, binds the
	// peer to A, answers once
	tr.feed(0, initFrame, "A")
	s := tr.sess.Load()
	if s == nil {
		t.Fatal("first Init did not install a session")
	}
	if c0.sent.Load() != 1 {
		t.Fatalf("first Init: want 1 Resp on carrier 0, got %d", c0.sent.Load())
	}
	if p := c0.peer.Load(); p == nil || *p != "A" {
		t.Fatalf("first Init did not bind the peer to A: %v", p)
	}
	// advance the send counter as if the server sent some records
	s.protect([]byte("x"))
	s.protect([]byte("y"))
	seqBefore := s.sendSeq.Load()

	// replay the SAME Init on carrier 0 from a DIFFERENT source "EVIL": no new
	// session, no reset, no answer, and crucially the peer must NOT move to EVIL.
	tr.feed(0, initFrame, "EVIL")
	if tr.sess.Load() != s {
		t.Fatal("a replayed Init replaced the live session")
	}
	if s.sendSeq.Load() != seqBefore {
		t.Fatalf("a replayed Init reset the send counter: %d -> %d", seqBefore, s.sendSeq.Load())
	}
	if p := c0.peer.Load(); p == nil || *p != "A" {
		t.Fatalf("a replayed Init moved the peer to %v; it must stay A", p)
	}
	// replay on another carrier gets silence
	tr.feed(1, initFrame, "EVIL")
	if c1.sent.Load() != 0 {
		t.Fatalf("a replayed Init from another carrier was answered (%d Resp); a replay prober must get silence", c1.sent.Load())
	}

	// a genuinely new client (new ephemeral) still installs a fresh session
	initFrame2, _, _ := makeInit(psk, srv.Pub)
	tr.feed(0, initFrame2, "B")
	if tr.sess.Load() == s {
		t.Fatal("a new client handshake did not install a new session")
	}
}

// Junk from an unknown source must not bind the peer, answer, or install a
// session; the authenticated client keeps working.
func TestJunkCannotHijackPeer(t *testing.T) {
	psk := []byte("junk-hijack-test-shared-key-00001")
	srv, _ := GenerateKey()
	c0 := &countingCarrier{}
	tr := newTransport(roleServer, psk, srv, [32]byte{}, []*carrierState{{c: c0}})

	// a real client handshakes from "A"
	initFrame, _, _ := makeInit(psk, srv.Pub)
	tr.feed(0, initFrame, "A")
	if p := c0.peer.Load(); p == nil || *p != "A" {
		t.Fatalf("peer not bound to the real client: %v", p)
	}
	sent := c0.sent.Load()

	// junk and random bytes from "EVIL": no peer change, no answer
	for _, junk := range [][]byte{[]byte("not a bridge frame at all"), randBytes(120), randBytes(400)} {
		tr.feed(0, junk, "EVIL")
	}
	if p := c0.peer.Load(); p == nil || *p != "A" {
		t.Fatalf("junk moved the peer to %v; it must stay A", p)
	}
	if c0.sent.Load() != sent {
		t.Fatalf("junk drew %d answer(s); it must be silent", c0.sent.Load()-sent)
	}
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	randFill(b)
	return b
}

func TestReplayWindow(t *testing.T) {
	var w replayWindow
	for _, seq := range []uint64{0, 1, 2, 5, 4, 3} {
		if !w.ok(seq) {
			t.Fatalf("seq %d should be accepted first time", seq)
		}
	}
	for _, seq := range []uint64{0, 1, 5, 3} {
		if w.ok(seq) {
			t.Fatalf("seq %d is a replay and must be refused", seq)
		}
	}
	if !w.ok(1000) {
		t.Fatal("a big jump forward should be accepted")
	}
	// within the wide window, an out-of-order seq is still accepted once
	if !w.ok(900) { // 100 below top, well inside the 4096 window: a real reorder, accept
		t.Fatal("an in-window out-of-order seq should be accepted")
	}
	if w.ok(900) { // but only once
		t.Fatal("the same in-window seq is a replay and must be refused")
	}
	if !w.ok(50000) { // jump far forward
		t.Fatal("a big jump forward should be accepted")
	}
	if w.ok(50000 - replaySpan - 1) { // now far below the window: refused
		t.Fatal("a seq past the window must be refused")
	}
	if w.maxReorder == 0 {
		t.Fatal("maxReorder should record the out-of-order gaps")
	}
}
