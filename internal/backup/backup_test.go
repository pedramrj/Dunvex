package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// AWS's own example ("GET Object" in the Signature Version 4 docs) signs to
// exactly this signature.
func TestSignatureV4Example(t *testing.T) {
	b := &Bucket{Access: "AKIAIOSFODNN7EXAMPLE", Secret: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", Region: "us-east-1",
		now: func() time.Time { return time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC) }}
	req, _ := http.NewRequest("GET", "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	req.Header.Set("Range", "bytes=0-9")
	b.sign(req, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, " +
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
}

func TestOpen(t *testing.T) {
	db := append([]byte("SQLite format 3\x00"), bytes.Repeat([]byte("dunvex"), 1000)...)
	if got, err := Open(db, ""); err != nil || !bytes.Equal(got, db) {
		t.Fatal("plain", err)
	}
	z := gz(db)
	if len(z) >= len(db) {
		t.Fatal("gzip did not shrink it")
	}
	if got, err := Open(z, ""); err != nil || !bytes.Equal(got, db) {
		t.Fatal("gzip", err)
	}
	enc, err := Encrypt(z, "correct horse")
	if err != nil || !Encrypted(enc) || bytes.Contains(enc, []byte("dunvex")) {
		t.Fatal("encrypt", err)
	}
	if got, err := Open(enc, "correct horse"); err != nil || !bytes.Equal(got, db) {
		t.Fatal("decrypt", err)
	}
	for _, pass := range []string{"", "wrong"} {
		if _, err := Open(enc, pass); !errors.Is(err, ErrPassphrase) {
			t.Fatalf("pass %q: %v", pass, err)
		}
	}
	enc[len(enc)-1] ^= 1 // one bit changed: refused, not half-restored
	if _, err := Open(enc, "correct horse"); !errors.Is(err, ErrPassphrase) {
		t.Fatal("tampered file opened")
	}
	if _, err := Open([]byte("not a backup at all"), ""); err == nil {
		t.Fatal("junk accepted")
	}
	for name, ok := range map[string]bool{"dunvex-1.2.3.4-20261124-120000.db.gz": true, "dunvex-20261124-120000.db.gz.enc": true,
		"before-restore-20261124-120000.db": true, "../dunvex-x.db": false, "dunvex-x.db/../../etc": false, "data.db": false, "dunvex-x.txt": false} {
		if Valid(name) != ok {
			t.Errorf("Valid(%q) = %v", name, !ok)
		}
	}
}

// fakeBucket keeps what is PUT, and checks the request is signed for it.
type fakeBucket struct {
	mu   sync.Mutex
	objs map[string][]byte
	fail bool
}

func (f *fakeBucket) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	sum := sha256.Sum256(body)
	if f.fail || r.Method != "PUT" || r.Header.Get("X-Amz-Content-Sha256") != hex.EncodeToString(sum[:]) ||
		!strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AK/") {
		w.WriteHeader(403)
		io.WriteString(w, `<?xml version="1.0"?><Error><Code>AccessDenied</Code><Message>Access Denied</Message></Error>`)
		return
	}
	f.mu.Lock()
	f.objs[r.URL.Path] = body
	f.mu.Unlock()
}

func TestService(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.CreateUser(&store.User{Name: "alice", UUID: util.UUID(), Password: "p", SubToken: "alice-token-0123456789", Enabled: true})
	bucket := &fakeBucket{objs: map[string][]byte{}}
	srv := httptest.NewServer(bucket)
	defer srv.Close()
	var sent []string
	s := &Service{St: st, Dir: filepath.Join(dir, "backups"), Name: func() string { return "203.0.113.7" },
		Telegram: func(name string, data []byte, caption string) (int, error) {
			sent = append(sent, name+"|"+caption)
			return 1, nil
		}}

	// by hand: kept here only
	r := s.Make(ReasonManual, false)
	if r.Error != "" || !strings.HasPrefix(r.File, "dunvex-203.0.113.7-") || !strings.HasSuffix(r.File, ".db.gz") || len(sent) != 0 || !r.OK() {
		t.Fatalf("manual: %+v", r)
	}
	data, _ := os.ReadFile(s.Path(r.File))
	db, err := Open(data, "")
	if err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(dir, "restored.db")
	os.WriteFile(restored, db, 0o600)
	st2, err := store.Open(restored)
	if err != nil {
		t.Fatal(err)
	}
	if u, err := st2.UserByName("alice"); err != nil || u.SubToken != "alice-token-0123456789" {
		t.Fatal("the backup does not hold alice", err)
	}
	st2.Close()

	// scheduled, with a passphrase and a bucket: encrypted, sent to Telegram and the bucket
	st.SetSetting(KeyPassphrase, "s3cret pass")
	st.SetSetting(KeyS3Endpoint, srv.URL)
	st.SetSetting(KeyS3Bucket, "vpn-backups")
	st.SetSetting(KeyS3Prefix, "/panel-1/")
	st.SetSetting(KeyS3Access, "AK")
	st.SetSetting(KeyS3Secret, "SK")
	time.Sleep(1100 * time.Millisecond) // a new second: a new file name
	r = s.Make(ReasonSchedule, true)
	if r.Error != "" || !r.Encrypted || !strings.HasSuffix(r.File, ".db.gz.enc") || r.Telegram != "ok" || r.S3 != "ok" {
		t.Fatalf("scheduled: %+v", r)
	}
	if len(sent) != 1 || !strings.Contains(sent[0], "🔒") || !strings.Contains(sent[0], "203.0.113.7") {
		t.Fatalf("telegram: %v", sent)
	}
	obj := bucket.objs["/vpn-backups/panel-1/"+r.File]
	if obj == nil || !Encrypted(obj) {
		t.Fatalf("bucket: %v", bucket.objs)
	}
	if _, err := Open(obj, "s3cret pass"); err != nil {
		t.Fatal("the copy in the bucket does not open", err)
	}

	// a copy that did not leave the server is an event (for the bot and webhooks)
	bucket.fail = true
	time.Sleep(1100 * time.Millisecond)
	r = s.Make(ReasonSchedule, true)
	if r.Error != "" || r.OK() || !strings.Contains(r.S3, "AccessDenied") {
		t.Fatalf("bucket down: %+v", r)
	}
	found := false
	for _, e := range st.Events(10) {
		found = found || (e.Kind == "backup" && strings.Contains(e.Message, "did not leave the server") && strings.Contains(e.Message, "AccessDenied"))
	}
	if !found {
		t.Fatal("no event for the failed copy")
	}
	if runs := s.Runs(); len(runs) != 3 || runs[0].At < runs[2].At {
		t.Fatalf("runs: %+v", runs)
	}

	// rotation keeps the newest; safety copies are kept apart
	st.SetSetting(KeyKeep, "2")
	os.WriteFile(filepath.Join(s.Dir, "before-restore-20260101-000000.db"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(s.Dir, "notes.txt"), []byte("x"), 0o600)
	time.Sleep(1100 * time.Millisecond)
	st.SetSetting(KeyPassphrase, "")
	s.Make(ReasonManual, false)
	var made, safety int
	for _, f := range s.List() {
		if f.Safety {
			safety++
		} else {
			made++
		}
	}
	if made != 2 || safety != 1 || s.Path("notes.txt") != "" || s.Path("../t.db") != "" {
		t.Fatalf("after rotation: %+v", s.List())
	}

	// schedule
	st.SetSetting(KeyHours, "0")
	if s.Next() != 0 {
		t.Fatal("off but scheduled")
	}
	st.SetSetting(KeyHours, "6")
	if n := s.Next(); n < time.Now().Unix()+5*3600 {
		t.Fatalf("next %d right after a scheduled backup", n)
	}
	st.SetSetting(KeyHours, "5") // not offered: the default
	if s.Hours() != DefaultHours {
		t.Fatal("hours")
	}
}
