package geoip

import (
	"bytes"
	"net"
	"os"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
)

func ip(s string) net.IP { return net.ParseIP(s) }

func TestBuildLookupAndFile(t *testing.T) {
	b := NewBuilder()
	b.Add(ip("1.0.0.0"), ip("1.0.0.255"), "AU")
	b.Add(ip("1.0.1.0"), ip("1.0.3.255"), "CN")
	b.Add(ip("2.144.0.0"), ip("2.147.255.255"), "ir") // lower case is fine
	b.Add(ip("5.0.0.0"), ip("5.0.0.255"), "ZZ")       // reserved: stays unknown
	b.Add(ip("255.0.0.0"), ip("255.255.255.255"), "US")
	b.Add(ip("2a01:4f8::"), ip("2a01:4f8:ffff:ffff:ffff:ffff:ffff:ffff"), "DE")
	b.Add(ip("2001:db8::"), ip("2001:db8::ff"), "NL") // smaller than a /64
	db := b.Build("test", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))

	cases := map[string]string{
		"0.255.255.255": "", "1.0.0.7": "AU", "1.0.2.9": "CN", "1.0.4.0": "", "2.145.10.1": "IR",
		"5.0.0.1": "", "255.255.255.255": "US", "::ffff:2.144.0.1": "IR",
		"2a01:4f8:c17:1::1": "DE", "2a01:4f9::1": "", "2001:db8::1": "NL", "::1": "",
	}
	check := func(db *DB) {
		for a, want := range cases {
			if got := db.Lookup(ip(a)); got != want {
				t.Errorf("%s: got %q, want %q", a, got, want)
			}
		}
	}
	check(db)

	var buf bytes.Buffer
	if err := db.Write(&buf); err != nil {
		t.Fatal(err)
	}
	back, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if back.Source != "test" || !back.Built.Equal(db.Built) || back.Countries() != 6 {
		t.Fatalf("header: %q %v %d", back.Source, back.Built, back.Countries())
	}
	check(back)

	if _, err := Read(bytes.NewReader([]byte("not gzip"))); err == nil {
		t.Error("garbage should not load")
	}
}

func TestOverlapsAreClipped(t *testing.T) {
	b := NewBuilder()
	b.Add(ip("10.0.0.0"), ip("10.0.0.255"), "AA")
	b.Add(ip("10.0.0.128"), ip("10.0.1.255"), "BB") // starts inside AA: AA keeps its half
	db := b.Build("", time.Time{})
	if db.Lookup(ip("10.0.0.200")) != "AA" || db.Lookup(ip("10.0.1.5")) != "BB" || db.Lookup(ip("10.0.2.0")) != "" {
		t.Fatalf("overlap: %q %q %q", db.Lookup(ip("10.0.0.200")), db.Lookup(ip("10.0.1.5")), db.Lookup(ip("10.0.2.0")))
	}
}

// a tiny geoip.dat: IR with one IPv4 and one IPv6 network, plus a "private" list
func TestFromXrayDat(t *testing.T) {
	cidr := func(addr net.IP, prefix int) []byte {
		var b []byte
		b = protowire.AppendTag(b, 1, protowire.BytesType)
		b = protowire.AppendBytes(b, addr)
		b = protowire.AppendTag(b, 2, protowire.VarintType)
		return protowire.AppendVarint(b, uint64(prefix))
	}
	entry := func(cc string, cidrs ...[]byte) []byte {
		var b []byte
		b = protowire.AppendTag(b, 1, protowire.BytesType)
		b = protowire.AppendString(b, cc)
		for _, c := range cidrs {
			b = protowire.AppendTag(b, 2, protowire.BytesType)
			b = protowire.AppendBytes(b, c)
		}
		return b
	}
	var dat []byte
	for _, e := range [][]byte{
		entry("IR", cidr(ip("2.144.0.0").To4(), 14), cidr(ip("2a01:5ec0::"), 29)),
		entry("PRIVATE", cidr(ip("10.0.0.0").To4(), 8)),
	} {
		dat = protowire.AppendTag(dat, 1, protowire.BytesType)
		dat = protowire.AppendBytes(dat, e)
	}
	db, err := FromXrayDat(dat)
	if err != nil {
		t.Fatal(err)
	}
	if db.Lookup(ip("2.147.1.1")) != "IR" || db.Lookup(ip("2a01:5ec0:1::5")) != "IR" || db.Lookup(ip("10.1.1.1")) != "" || db.Lookup(ip("2.148.0.0")) != "" {
		t.Fatal("lookups from geoip.dat")
	}
	if _, err := FromXrayDat([]byte{0xff}); err == nil {
		t.Error("garbage should not load")
	}
}

func TestPoint(t *testing.T) {
	if lat, lon, ok := Point("ir"); !ok || lat < 25 || lat > 40 || lon < 44 || lon > 63 {
		t.Fatalf("Iran: %v %v %v", lat, lon, ok)
	}
	if _, _, ok := Point("XX"); ok {
		t.Error("unknown code")
	}
}

// With DUNVEX_TEST_GEOIP pointing at a real geoip-country.bin.gz, well-known
// addresses must land in the right country.
func TestRealFile(t *testing.T) {
	path := os.Getenv("DUNVEX_TEST_GEOIP")
	if path == "" {
		t.Skip("DUNVEX_TEST_GEOIP not set")
	}
	start := time.Now()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("loaded %s in %v", db.Source, time.Since(start))
	for a, want := range map[string]string{"8.8.8.8": "US", "91.107.138.242": "DE", "2.144.0.1": "IR", "5.160.0.1": "IR", "2a01:4f8::1": "DE"} {
		if got := db.Lookup(ip(a)); got != want {
			t.Errorf("%s: got %q, want %q", a, got, want)
		}
	}
	for _, cc := range []string{"US", "DE", "IR", "NL", "FI", "TR", "AE", "GB", "FR"} {
		if _, _, ok := Point(cc); !ok {
			t.Errorf("no map point for %s", cc)
		}
	}
}
