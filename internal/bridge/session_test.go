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
	resp, ss, err := readInit(psk, srv, initFrame)
	if err != nil {
		t.Fatalf("server refused a valid init: %v", err)
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
	// A prober with the wrong PSK: every init is refused with silence.
	initFrame, _, err := makeInit([]byte("the-wrong-pre-shared-key-value!!"), srv.Pub)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := readInit([]byte("the-right-pre-shared-key-value!!"), srv, initFrame); err != errSilent {
		t.Fatalf("want errSilent for a wrong-key init, got %v", err)
	}
	// Random junk is also silent, never an answer.
	junk := make([]byte, 200)
	randFill(junk)
	if _, _, err := readInit([]byte("the-right-pre-shared-key-value!!"), srv, junk); err != errSilent {
		t.Fatalf("want errSilent for junk, got %v", err)
	}
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
	if w.ok(900) {
		t.Fatal("a seq far below the window must be refused")
	}
}
