package geodata

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadWhatWasWritten(t *testing.T) {
	dir := t.TempDir()
	site, ip := filepath.Join(dir, "geosite.dat"), filepath.Join(dir, "geoip.dat")
	want := []Domain{{Suffix, "aparat.com"}, {Full, "www.example.ir"}, {Keyword, "digikala"}, {Regex, `^cdn\d+\.x\.com$`}}
	if err := WriteSite(site, map[string][]Domain{"CATEGORY-IR": want, "google": {{Suffix, "google.com"}}}); err != nil {
		t.Fatal(err)
	}
	if err := WriteIP(ip, map[string][]string{"IR": {"2.144.0.0/14", "2a01:5ec0::/29"}, "private": {"10.0.0.0/8"}}); err != nil {
		t.Fatal(err)
	}
	got, err := Site(site, "category-ir") // codes are matched without case
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("site: %v %+v", err, got)
	}
	if _, err := Site(site, "netflix"); !errors.Is(err, ErrNoCode) {
		t.Errorf("a missing code: %v", err)
	}
	nets, err := IP(ip, "ir")
	if err != nil || len(nets) != 2 || nets[0].String() != "2.144.0.0/14" || nets[1].String() != "2a01:5ec0::/29" {
		t.Fatalf("ip: %v %v", err, nets)
	}
	codes, _ := Codes(site)
	if !reflect.DeepEqual(codes, []string{"category-ir", "google"}) {
		t.Errorf("codes: %v", codes)
	}
	if _, err := Site(filepath.Join(dir, "missing.dat"), "x"); err == nil {
		t.Error("a missing file read fine")
	}
}

func TestBrokenFile(t *testing.T) {
	if err := fields([]byte{0x0a, 0x10, 0x01}, func(int, int, uint64, []byte) error { return nil }); err == nil {
		t.Error("a truncated message was accepted")
	}
}
