package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pedramrj/dunvex/internal/backup"
	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/util"
)

func TestBackupAPI(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hash, _ := HashPassword("owner-pass-1")
	st.UpsertAdmin("boss", hash)
	ahash, _ := HashPassword("admin-pass-1")
	st.CreateAdmin(&store.Admin{Username: "helper", PasswordHash: ahash, Role: store.RoleAdmin, Enabled: true})
	st.SetSetting("host", "203.0.113.7")
	st.CreateUser(&store.User{Name: "alice", UUID: util.UUID(), Password: "p", SubToken: "alice-token-0123456789", Enabled: true})

	var mu sync.Mutex
	objs := map[string][]byte{}
	bucket := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AKEY/") {
			w.WriteHeader(403)
			return
		}
		mu.Lock()
		objs[r.URL.Path] = b
		mu.Unlock()
	}))
	defer bucket.Close()

	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler()}
	srv.Mgr = engine.NewManager(st, dir, filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	owner, helper := newClient(t, ts.URL), newClient(t, ts.URL)
	owner.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"})
	helper.do("POST", "/login", map[string]string{"username": "helper", "password": "admin-pass-1"})
	if code, _ := helper.do("GET", "/backups", nil); code != 403 {
		t.Fatalf("an admin read the backups: %d", code)
	}

	_, out := owner.do("GET", "/backups", nil)
	set := out["settings"].(map[string]any)
	if set["hours"] != 6.0 || set["keep"] != 14.0 || set["telegram"] != true || set["encrypted"] != false {
		t.Fatalf("defaults: %v", set)
	}
	for _, bad := range []map[string]any{
		{"hours": 5}, {"keep": 0}, {"passphrase": "short"},
		{"s3": map[string]any{"endpoint": "s3.example.com", "bucket": "b", "access": "a", "secret": "s"}},
		{"s3": map[string]any{"endpoint": "https://s3.example.com", "bucket": "b", "access": "a"}},
	} {
		if code, _ := owner.do("PUT", "/backups/settings", bad); code != 400 {
			t.Errorf("accepted %v", bad)
		}
	}
	s3 := map[string]any{"endpoint": bucket.URL + "/", "region": "auto", "bucket": "dvx", "prefix": "panel", "access": "AKEY", "secret": "the-secret"}
	if code, out := owner.do("PUT", "/backups/settings", map[string]any{"hours": 12, "keep": 5, "passphrase": "long enough pass", "s3": s3}); code != 200 {
		t.Fatalf("settings: %d %v", code, out)
	}
	_, out = owner.do("GET", "/backups", nil)
	if js, _ := json.Marshal(out); strings.Contains(string(js), "the-secret") || strings.Contains(string(js), "long enough pass") {
		t.Fatalf("a secret came back: %s", js)
	}
	set = out["settings"].(map[string]any)
	if set["hours"] != 12.0 || set["encrypted"] != true || set["s3"].(map[string]any)["secretSet"] != true {
		t.Fatalf("saved: %v", set)
	}
	// the check reuses the saved secret
	s3["secret"] = ""
	if code, out := owner.do("POST", "/backups/check-bucket", s3); code != 200 || objs["/dvx/panel/dunvex-connection-check.txt"] == nil {
		t.Fatalf("check: %d %v %v", code, out, objs)
	}

	// a backup now: encrypted, kept, in the bucket; no bot, so Telegram is skipped quietly
	code, out := owner.do("POST", "/backups", map[string]any{"offsite": true})
	if code != 200 || out["s3"] != "ok" || out["telegram"] != nil || out["encrypted"] != true {
		t.Fatalf("backup: %d %v", code, out)
	}
	name := out["file"].(string)
	if objs["/dvx/panel/"+name] == nil {
		t.Fatal("not in the bucket")
	}
	for _, e := range st.Events(10) {
		if e.Kind == "backup" {
			t.Fatalf("an event for a backup that worked: %s", e.Message)
		}
	}
	res, _ := http.Get(owner.base + "/backups/" + name)
	if res.StatusCode != 401 && res.StatusCode != 403 {
		t.Fatalf("download without signing in: %d", res.StatusCode)
	}
	if code, _ := owner.do("GET", "/backups/..%2Ft.db", nil); code != 404 {
		t.Fatalf("a path outside the folder: %d", code)
	}

	// restore from the list (the saved passphrase opens it)
	if code, out := owner.do("POST", "/backups/"+name+"/restore", nil); code != 200 {
		t.Fatalf("restore: %d %v", code, out)
	}
	pending, _ := os.ReadFile(filepath.Join(dir, "restore.pending"))
	st2, err := store.Open(strings.TrimSpace(string(pending)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st2.UserByName("alice"); err != nil {
		t.Fatal("the restored database lacks alice")
	}
	st2.Close()
	if fs := srv.backups().List(); len(fs) != 2 || !fs[0].Safety && !fs[1].Safety {
		t.Fatalf("no safety copy: %+v", fs)
	}

	// an encrypted upload after the passphrase is gone from the server: asked for
	enc, _ := os.ReadFile(srv.backups().Path(name))
	owner.do("PUT", "/backups/settings", map[string]any{"clearPassphrase": true})
	upload := func(pass string) (int, map[string]any) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile("file", name)
		fw.Write(enc)
		if pass != "" {
			mw.WriteField("passphrase", pass)
		}
		mw.Close()
		req, _ := http.NewRequest("POST", owner.base+"/restore", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("X-Dunvex", "1")
		res, err := owner.hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var m map[string]any
		json.NewDecoder(res.Body).Decode(&m)
		return res.StatusCode, m
	}
	if code, m := upload(""); code != 400 || m["needPassphrase"] != true {
		t.Fatalf("no passphrase: %d %v", code, m)
	}
	if code, m := upload("long enough pass"); code != 200 {
		t.Fatalf("with the passphrase: %d %v", code, m)
	}
	if _, err := backup.Open(enc, "long enough pass"); err != nil {
		t.Fatal(err)
	}
}
