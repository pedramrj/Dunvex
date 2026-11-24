// Package backup makes Dunvex's backups on a schedule: a consistent copy of
// the database, gzipped and (with a passphrase) encrypted, kept on the server
// in rotation and sent off it, to the Telegram bot's admins and to an
// S3-compatible bucket, so a server that is lost does not take the panel's
// users, keys and settings with it.
package backup

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/scrypt"

	"github.com/pedramrj/dunvex/internal/store"
)

// Settings.
const (
	KeyHours      = "backup_hours"      // every this many hours (0 = only by hand)
	KeyKeep       = "backup_keep"       // backups kept on the server
	KeyTelegram   = "backup_telegram"   // "true": each scheduled backup goes to the bot's admins
	KeyPassphrase = "backup_passphrase" // when set, every backup is encrypted with it
	KeyS3Endpoint = "backup_s3_endpoint"
	KeyS3Region   = "backup_s3_region"
	KeyS3Bucket   = "backup_s3_bucket"
	KeyS3Prefix   = "backup_s3_prefix"
	KeyS3Access   = "backup_s3_access"
	KeyS3Secret   = "backup_s3_secret"
	keyRuns       = "backup_runs" // the last runs (JSON)

	DefaultHours = 6
	DefaultKeep  = 14
)

// HourChoices are the schedules offered.
var HourChoices = map[int]bool{0: true, 1: true, 2: true, 3: true, 6: true, 12: true, 24: true}

// Reasons a backup is made.
const (
	ReasonSchedule = "schedule"
	ReasonManual   = "manual"
	ReasonUpdate   = "update" // before the panel updates itself
)

// Run is one backup and where it went.
type Run struct {
	At        int64  `json:"at"`
	Reason    string `json:"reason"`
	File      string `json:"file,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Encrypted bool   `json:"encrypted,omitempty"`
	Telegram  string `json:"telegram,omitempty"` // "ok", the error, or "" when not sent there
	S3        string `json:"s3,omitempty"`       // the same for the bucket
	Error     string `json:"error,omitempty"`    // the backup itself failed
}

// OK: made, and every copy that was due arrived.
func (r Run) OK() bool {
	return r.Error == "" && (r.Telegram == "" || r.Telegram == "ok") && (r.S3 == "" || r.S3 == "ok")
}

// File is a backup kept on the server.
type File struct {
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	At        int64  `json:"at"`
	Encrypted bool   `json:"encrypted,omitempty"`
	Safety    bool   `json:"safety,omitempty"` // the copy taken just before a restore
}

// Service makes and keeps the backups.
type Service struct {
	St   *store.Store
	Dir  string
	Name func() string // the server's name in file names and captions ("" = none)
	// Telegram sends a file to the bot's admins and says to how many (nil: no bot).
	Telegram func(name string, data []byte, caption string) (int, error)

	mu    sync.Mutex // one backup at a time
	ready time.Time  // no scheduled backup before this
}

func (s *Service) setting(k, def string) string { return s.St.GetSetting(k, def) }

func (s *Service) intSetting(k string, def int) int {
	n, err := strconv.Atoi(s.setting(k, strconv.Itoa(def)))
	if err != nil {
		return def
	}
	return n
}

// Hours is the schedule (0 = off).
func (s *Service) Hours() int {
	if h := s.intSetting(KeyHours, DefaultHours); HourChoices[h] {
		return h
	}
	return DefaultHours
}

// Keep is how many backups stay on the server.
func (s *Service) Keep() int {
	if k := s.intSetting(KeyKeep, DefaultKeep); k >= 1 && k <= 500 {
		return k
	}
	return DefaultKeep
}

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9.-]+`)

func (s *Service) server() string {
	if s.Name == nil {
		return ""
	}
	n := strings.Trim(unsafeName.ReplaceAllString(s.Name(), "-"), "-.")
	if len(n) > 40 {
		n = n[:40]
	}
	return n
}

// Make takes a backup now; offsite sends it to Telegram and the bucket too.
func (s *Service) Make(reason string, offsite bool) Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := Run{At: time.Now().Unix(), Reason: reason}
	defer func() { s.record(run) }()
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		run.Error = err.Error()
		return run
	}
	data, err := s.snapshot()
	if err != nil {
		run.Error = "copying the database: " + err.Error()
		return run
	}
	data = gz(data)
	name := "dunvex-"
	if srv := s.server(); srv != "" {
		name += srv + "-"
	}
	name += time.Unix(run.At, 0).Format("20060102-150405") + ".db.gz"
	if pass := s.setting(KeyPassphrase, ""); pass != "" {
		if data, err = Encrypt(data, pass); err != nil {
			run.Error = err.Error()
			return run
		}
		name += ".enc"
		run.Encrypted = true
	}
	path := filepath.Join(s.Dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		run.Error = err.Error()
		return run
	}
	run.File, run.Size = name, int64(len(data))
	s.rotate()
	if offsite {
		s.offsite(&run, name, data)
	}
	return run
}

// offsite sends a backup to the places the owner chose.
func (s *Service) offsite(run *Run, name string, data []byte) {
	caption := "💾 Dunvex backup · " + time.Unix(run.At, 0).Format("2006-01-02 15:04")
	if srv := s.server(); srv != "" {
		caption += " · " + srv
	}
	if run.Encrypted {
		caption += " · 🔒"
	}
	if s.setting(KeyTelegram, "true") == "true" && s.Telegram != nil {
		if _, err := s.Telegram(name, data, caption); errors.Is(err, ErrNoTelegram) {
			// no bot: nothing was due there
		} else if err != nil {
			run.Telegram = err.Error()
		} else {
			run.Telegram = "ok"
		}
	}
	if b := s.Bucket(); b != nil {
		if err := b.Put(name, data); err != nil {
			run.S3 = err.Error()
		} else {
			run.S3 = "ok"
		}
	}
}

// snapshot is a consistent copy of the database.
func (s *Service) snapshot() ([]byte, error) {
	tmp := filepath.Join(s.Dir, fmt.Sprintf(".snapshot-%d.db", time.Now().UnixNano()))
	defer os.Remove(tmp)
	if err := s.St.Backup(tmp); err != nil {
		return nil, err
	}
	return os.ReadFile(tmp)
}

func gz(b []byte) []byte {
	var buf bytes.Buffer
	w, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

// rotate keeps the newest backups (and the last three safety copies).
func (s *Service) rotate() {
	var made, safety []File
	for _, f := range s.List() {
		if f.Safety {
			safety = append(safety, f)
		} else {
			made = append(made, f)
		}
	}
	for i, f := range made {
		if i >= s.Keep() {
			os.Remove(filepath.Join(s.Dir, f.Name))
		}
	}
	for i, f := range safety {
		if i >= 3 {
			os.Remove(filepath.Join(s.Dir, f.Name))
		}
	}
}

// List is the backups on the server, newest first.
func (s *Service) List() []File {
	entries, _ := os.ReadDir(s.Dir)
	out := []File{}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !Valid(n) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, File{Name: n, Size: info.Size(), At: info.ModTime().Unix(),
			Encrypted: strings.HasSuffix(n, ".enc"), Safety: strings.HasPrefix(n, "before-restore-")})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].At > out[j].At || (out[i].At == out[j].At && out[i].Name > out[j].Name)
	})
	return out
}

var validName = regexp.MustCompile(`^(dunvex|before-restore)-[a-zA-Z0-9.-]*\.db(\.gz)?(\.enc)?$`)

// Valid says whether name is a backup file of this folder (and safe to open).
func Valid(name string) bool {
	return validName.MatchString(name) && !strings.Contains(name, "..")
}

// Path is where a backup is, or "" when there is no such file.
func (s *Service) Path(name string) string {
	if !Valid(name) {
		return ""
	}
	p := filepath.Join(s.Dir, name)
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// ---------------------------------------------------------------- history

func (s *Service) record(r Run) {
	runs := s.Runs()
	runs = append([]Run{r}, runs...)
	if len(runs) > 30 {
		runs = runs[:30]
	}
	b, _ := json.Marshal(runs)
	s.St.SetSetting(keyRuns, string(b))
	switch {
	case r.Error != "":
		s.St.Log("backup", "backup failed: %s", r.Error)
	case !r.OK():
		var where []string
		if r.Telegram != "" && r.Telegram != "ok" {
			where = append(where, "Telegram: "+r.Telegram)
		}
		if r.S3 != "" && r.S3 != "ok" {
			where = append(where, "bucket: "+r.S3)
		}
		s.St.Log("backup", "backup %s made, but it did not leave the server: %s", r.File, strings.Join(where, "; "))
	}
}

// Runs is the last backups, newest first.
func (s *Service) Runs() []Run {
	var runs []Run
	json.Unmarshal([]byte(s.setting(keyRuns, "[]")), &runs)
	if runs == nil {
		runs = []Run{}
	}
	return runs
}

// Next is when the next scheduled backup is due (0 = none scheduled).
func (s *Service) Next() int64 {
	h := s.Hours()
	if h == 0 {
		return 0
	}
	last := int64(0)
	for _, r := range s.Runs() {
		if r.Reason == ReasonSchedule && r.Error == "" {
			last = r.At
			break
		}
	}
	next := last + int64(h)*3600
	if r := s.ready.Unix(); next < r {
		next = r
	}
	return next
}

// Loop makes the scheduled backups until stop closes. The first one waits a
// few minutes after a start, so a restart loop does not fill the folder.
func (s *Service) Loop(stop <-chan struct{}) {
	s.ready = time.Now().Add(5 * time.Minute)
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		if n := s.Next(); n != 0 && time.Now().Unix() >= n {
			s.Make(ReasonSchedule, true)
		}
	}
}

// ---------------------------------------------------------------- reading a backup

// ErrNoTelegram: the bot is not set up; scheduled copies skip it quietly.
var ErrNoTelegram = errors.New("the Telegram bot is not set up")

var (
	encMagic = []byte("DUNVEXENC1")
	// ErrPassphrase: the file is encrypted, and the passphrase is missing or wrong.
	ErrPassphrase = errors.New("this backup is encrypted: the passphrase is missing or wrong")
)

// Encrypt seals data with a key made from pass (scrypt, AES-256-GCM).
func Encrypt(data []byte, pass string) ([]byte, error) {
	salt := make([]byte, 16)
	rand.Read(salt)
	key, err := scrypt.Key([]byte(pass), salt, 1<<15, 8, 1, 32)
	if err != nil {
		return nil, err
	}
	block, _ := aes.NewCipher(key)
	g, _ := cipher.NewGCM(block)
	nonce := make([]byte, g.NonceSize())
	rand.Read(nonce)
	out := append(append(append([]byte{}, encMagic...), salt...), nonce...)
	return g.Seal(out, nonce, data, encMagic), nil
}

// Encrypted says whether data is an encrypted backup.
func Encrypted(data []byte) bool { return bytes.HasPrefix(data, encMagic) }

// Open turns a backup in any of its forms (plain, gzipped, encrypted) back
// into the database file, checking that it is one.
func Open(data []byte, pass string) ([]byte, error) {
	if Encrypted(data) {
		if pass == "" || len(data) < len(encMagic)+16+12+16 {
			return nil, ErrPassphrase
		}
		salt := data[len(encMagic) : len(encMagic)+16]
		key, err := scrypt.Key([]byte(pass), salt, 1<<15, 8, 1, 32)
		if err != nil {
			return nil, err
		}
		block, _ := aes.NewCipher(key)
		g, _ := cipher.NewGCM(block)
		rest := data[len(encMagic)+16:]
		plain, err := g.Open(nil, rest[:g.NonceSize()], rest[g.NonceSize():], encMagic)
		if err != nil {
			return nil, ErrPassphrase
		}
		data = plain
	}
	if len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b {
		r, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		plain, err := io.ReadAll(io.LimitReader(r, 2<<30))
		if err != nil {
			return nil, fmt.Errorf("the backup is damaged: %v", err)
		}
		data = plain
	}
	if !bytes.HasPrefix(data, []byte("SQLite format 3\x00")) {
		return nil, errors.New("this is not a Dunvex backup")
	}
	return data, nil
}
