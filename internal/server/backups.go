package server

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pedramrj/dunvex/internal/backup"
)

// Automatic backups (see the backup package): the schedule, the copies on
// the server, and where they go. Owner only: a backup holds everything.

// FileSender is the bot's side for backups: files to its admins.
type FileSender interface {
	SendFileToAdmins(name string, data []byte, caption string) (int, error)
}

func (s *Server) backups() *backup.Service {
	s.backupOnce.Do(func() {
		s.Backups = &backup.Service{St: s.St, Dir: s.BackupDir(), Name: func() string { return s.Env().Host },
			Telegram: func(name string, data []byte, caption string) (int, error) {
				fs, ok := s.Bot.(FileSender)
				if !ok || s.Bot == nil {
					return 0, backup.ErrNoTelegram
				}
				if st := s.Bot.Status(); st["configured"] != true {
					return 0, backup.ErrNoTelegram
				}
				return fs.SendFileToAdmins(name, data, caption)
			}}
	})
	return s.Backups
}

// RunBackups makes the scheduled backups until stop closes.
func (s *Server) RunBackups(stop <-chan struct{}) { s.backups().Loop(stop) }

type bucketView struct {
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"`
	Access    string `json:"access"`
	SecretSet bool   `json:"secretSet"` // the secret is never sent back
}

func (s *Server) getBackups(w http.ResponseWriter, r *http.Request) {
	b := s.backups()
	get := func(k, def string) string { return s.St.GetSetting(k, def) }
	tg := map[string]any{"configured": false, "admins": 0}
	if s.Bot != nil {
		st := s.Bot.Status()
		tg = map[string]any{"configured": st["configured"], "admins": st["admins"]}
	}
	writeJSON(w, 200, map[string]any{
		"settings": map[string]any{
			"hours": b.Hours(), "keep": b.Keep(), "telegram": get(backup.KeyTelegram, "true") == "true",
			"encrypted": get(backup.KeyPassphrase, "") != "",
			"s3": bucketView{Endpoint: get(backup.KeyS3Endpoint, ""), Region: get(backup.KeyS3Region, ""), Bucket: get(backup.KeyS3Bucket, ""),
				Prefix: get(backup.KeyS3Prefix, ""), Access: get(backup.KeyS3Access, ""), SecretSet: get(backup.KeyS3Secret, "") != ""},
		},
		"files": b.List(), "runs": b.Runs(), "next": b.Next(), "telegram": tg,
	})
}

type bucketIn struct {
	Endpoint, Region, Bucket, Prefix, Access string
	Secret                                   string // "" keeps the saved one
}

func (s *Server) putBackupSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Hours           *int      `json:"hours"`
		Keep            *int      `json:"keep"`
		Telegram        *bool     `json:"telegram"`
		Passphrase      string    `json:"passphrase"`      // a new passphrase ("" leaves it)
		ClearPassphrase bool      `json:"clearPassphrase"` // backups from now on are not encrypted
		S3              *bucketIn `json:"s3"`
		ClearS3         bool      `json:"clearS3"`
	}
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	if in.Hours != nil && !backup.HourChoices[*in.Hours] {
		fail(w, 400, "backups run every 1, 2, 3, 6, 12 or 24 hours, or 0 for only by hand")
		return
	}
	if in.Keep != nil && (*in.Keep < 1 || *in.Keep > 500) {
		fail(w, 400, "keep between 1 and 500 backups")
		return
	}
	if p := in.Passphrase; p != "" && len([]rune(p)) < 8 {
		fail(w, 400, "the passphrase needs at least 8 characters")
		return
	}
	if in.S3 != nil && !in.ClearS3 {
		if err := backup.CheckBucket(strings.TrimSpace(in.S3.Endpoint), strings.TrimSpace(in.S3.Bucket)); err != nil {
			fail(w, 400, err.Error())
			return
		}
		if strings.TrimSpace(in.S3.Access) == "" || (in.S3.Secret == "" && s.St.GetSetting(backup.KeyS3Secret, "") == "") {
			fail(w, 400, "the bucket needs its access key and secret")
			return
		}
	}
	if in.Hours != nil {
		s.St.SetSetting(backup.KeyHours, strconv.Itoa(*in.Hours))
	}
	if in.Keep != nil {
		s.St.SetSetting(backup.KeyKeep, strconv.Itoa(*in.Keep))
	}
	if in.Telegram != nil {
		s.St.SetSetting(backup.KeyTelegram, strconv.FormatBool(*in.Telegram))
	}
	switch {
	case in.ClearPassphrase:
		s.St.SetSetting(backup.KeyPassphrase, "")
	case in.Passphrase != "":
		s.St.SetSetting(backup.KeyPassphrase, in.Passphrase)
	}
	switch {
	case in.ClearS3:
		for _, k := range []string{backup.KeyS3Endpoint, backup.KeyS3Region, backup.KeyS3Bucket, backup.KeyS3Prefix, backup.KeyS3Access, backup.KeyS3Secret} {
			s.St.SetSetting(k, "")
		}
	case in.S3 != nil:
		s.St.SetSetting(backup.KeyS3Endpoint, strings.TrimRight(strings.TrimSpace(in.S3.Endpoint), "/"))
		s.St.SetSetting(backup.KeyS3Region, strings.TrimSpace(in.S3.Region))
		s.St.SetSetting(backup.KeyS3Bucket, strings.TrimSpace(in.S3.Bucket))
		s.St.SetSetting(backup.KeyS3Prefix, strings.Trim(strings.TrimSpace(in.S3.Prefix), "/"))
		s.St.SetSetting(backup.KeyS3Access, strings.TrimSpace(in.S3.Access))
		if in.S3.Secret != "" {
			s.St.SetSetting(backup.KeyS3Secret, strings.TrimSpace(in.S3.Secret))
		}
	}
	s.St.Log("settings", "%s changed the backup settings", adminOf(r).Username)
	s.getBackups(w, r)
}

// checkBucket stores a small file in the bucket, so the owner knows the
// settings work before the first backup needs them.
func (s *Server) checkBucket(w http.ResponseWriter, r *http.Request) {
	var in bucketIn
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	if err := backup.CheckBucket(strings.TrimSpace(in.Endpoint), strings.TrimSpace(in.Bucket)); err != nil {
		fail(w, 400, err.Error())
		return
	}
	secret := strings.TrimSpace(in.Secret)
	if secret == "" {
		secret = s.St.GetSetting(backup.KeyS3Secret, "")
	}
	b := &backup.Bucket{Endpoint: strings.TrimRight(strings.TrimSpace(in.Endpoint), "/"), Region: strings.TrimSpace(in.Region),
		Name: strings.TrimSpace(in.Bucket), Prefix: strings.Trim(strings.TrimSpace(in.Prefix), "/"), Access: strings.TrimSpace(in.Access), Secret: secret}
	if err := b.Put("dunvex-connection-check.txt", []byte("Dunvex can write backups here. "+time.Now().UTC().Format(time.RFC3339)+"\n")); err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) makeBackup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Offsite bool `json:"offsite"`
	}
	readJSON(r, &in)
	run := s.backups().Make(backup.ReasonManual, in.Offsite)
	if run.Error != "" {
		fail(w, 500, run.Error)
		return
	}
	writeJSON(w, 200, run)
}

func (s *Server) downloadBackup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	p := s.backups().Path(name)
	if p == "" {
		fail(w, 404, "no such backup")
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, p)
}

func (s *Server) deleteBackup(w http.ResponseWriter, r *http.Request) {
	p := s.backups().Path(r.PathValue("name"))
	if p == "" {
		fail(w, 404, "no such backup")
		return
	}
	if err := os.Remove(p); err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) restoreStored(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	p := s.backups().Path(name)
	if p == "" {
		fail(w, 404, "no such backup")
		return
	}
	var in struct {
		Passphrase string `json:"passphrase"`
	}
	readJSON(r, &in)
	data, err := os.ReadFile(p)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.restoreFrom(w, r, data, in.Passphrase, name)
}

// restoreFrom opens a backup (plain, gzipped or encrypted: the passphrase
// given, else the saved one), keeps a safety copy of the database, and
// swaps it in at a restart.
func (s *Server) restoreFrom(w http.ResponseWriter, r *http.Request, data []byte, pass, from string) {
	if pass == "" {
		pass = s.St.GetSetting(backup.KeyPassphrase, "")
	}
	db, err := backup.Open(data, pass)
	if err != nil {
		code := 400
		if errors.Is(err, backup.ErrPassphrase) {
			writeJSON(w, 400, map[string]any{"error": err.Error(), "needPassphrase": true})
			return
		}
		fail(w, code, err.Error())
		return
	}
	ensureDir(s.BackupDir())
	tmp := filepath.Join(s.BackupDir(), "restore-upload.db")
	if err := os.WriteFile(tmp, db, 0o600); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.St.Backup(filepath.Join(s.BackupDir(), "before-restore-"+time.Now().Format("20060102-150405")+".db"))
	os.WriteFile(filepath.Join(s.Home, "restore.pending"), []byte(tmp), 0o600)
	s.St.Log("settings", "%s restored the database from %s; the panel restarts", adminOf(r).Username, from)
	writeJSON(w, 200, map[string]bool{"ok": true})
	if s.Restart != nil {
		go func() { time.Sleep(time.Second); s.Restart() }()
	}
}

// restore takes an uploaded backup: .db, .db.gz or an encrypted .enc.
func (s *Server) restore(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 512<<20)
	f, hdr, err := r.FormFile("file")
	if err != nil {
		fail(w, 400, "upload a backup file (.db, .db.gz or .enc)")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		fail(w, 400, "the upload did not finish")
		return
	}
	s.restoreFrom(w, r, data, r.FormValue("passphrase"), "an upload ("+hdr.Filename+")")
}
