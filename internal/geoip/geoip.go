// Package geoip finds the country of an IP address offline.
//
// The data is a compact file of address ranges (geoip-country.bin.gz, made from
// DB-IP's free country database by scripts/geoipgen and published with every
// release) or, as a fallback, the geoip.dat that ships with Xray (it covers
// fewer countries). Nothing is looked up over the network: client addresses
// never leave the server.
package geoip

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	magic   = "DVXGEO1\n"
	unknown = 255
	maxCode = 254
)

// DB answers country lookups from sorted range starts.
type DB struct {
	Source string    // where the data came from, for the attribution line
	Built  time.Time // when the data was made

	codes []string
	s4    []uint32
	c4    []uint8
	s6    []uint64 // first 64 bits of the address: enough to tell countries apart
	c6    []uint8
}

// Lookup returns the ISO country code of ip, or "" when it is not known.
func (db *DB) Lookup(ip net.IP) string {
	if db == nil || ip == nil {
		return ""
	}
	var i int
	var c []uint8
	if v4 := ip.To4(); v4 != nil {
		a := binary.BigEndian.Uint32(v4)
		i = sort.Search(len(db.s4), func(k int) bool { return db.s4[k] > a }) - 1
		c = db.c4
	} else if v6 := ip.To16(); v6 != nil {
		a := binary.BigEndian.Uint64(v6[:8])
		i = sort.Search(len(db.s6), func(k int) bool { return db.s6[k] > a }) - 1
		c = db.c6
	}
	if i < 0 || i >= len(c) || c[i] == unknown || int(c[i]) >= len(db.codes) {
		return ""
	}
	return db.codes[c[i]]
}

// Ranges reports how many IPv4 and IPv6 ranges the data holds.
func (db *DB) Ranges() (v4, v6 int) { return len(db.s4), len(db.s6) }

// Countries is the number of countries in the data.
func (db *DB) Countries() int { return len(db.codes) }

// Point is where a country sits on the map.
func Point(cc string) (lat, lon float64, ok bool) {
	p, ok := points[strings.ToUpper(cc)]
	return float64(p[0]), float64(p[1]), ok
}

// ---------------------------------------------------------------- building

// Builder collects ranges and turns them into a DB.
type Builder struct {
	idx map[string]uint8
	cc  []string
	r4  []rng
	r6  []rng
}

type rng struct {
	start, end uint64
	c          uint8
}

// NewBuilder starts an empty data set.
func NewBuilder() *Builder { return &Builder{idx: map[string]uint8{}} }

func (b *Builder) code(cc string) (uint8, bool) {
	cc = strings.ToUpper(strings.TrimSpace(cc))
	if len(cc) != 2 || cc == "ZZ" {
		return 0, false // reserved or unknown space stays unknown
	}
	if i, ok := b.idx[cc]; ok {
		return i, true
	}
	if len(b.cc) >= maxCode {
		return 0, false
	}
	i := uint8(len(b.cc))
	b.idx[cc] = i
	b.cc = append(b.cc, cc)
	return i, true
}

// Add records that start..end (inclusive, both IPv4 or both IPv6) belong to cc.
func (b *Builder) Add(start, end net.IP, cc string) error {
	i, ok := b.code(cc)
	if !ok {
		return nil
	}
	if s4, e4 := start.To4(), end.To4(); s4 != nil && e4 != nil {
		s, e := binary.BigEndian.Uint32(s4), binary.BigEndian.Uint32(e4)
		if e < s {
			return fmt.Errorf("range %s-%s runs backwards", start, end)
		}
		b.r4 = append(b.r4, rng{uint64(s), uint64(e), i})
		return nil
	}
	s6, e6 := start.To16(), end.To16()
	if s6 == nil || e6 == nil || start.To4() != nil || end.To4() != nil {
		return fmt.Errorf("range %s-%s mixes address families", start, end)
	}
	s, e := binary.BigEndian.Uint64(s6[:8]), binary.BigEndian.Uint64(e6[:8])
	if e < s {
		return fmt.Errorf("range %s-%s runs backwards", start, end)
	}
	b.r6 = append(b.r6, rng{s, e, i})
	return nil
}

// AddCIDR records a network.
func (b *Builder) AddCIDR(n *net.IPNet, cc string) error {
	start := n.IP.Mask(n.Mask)
	end := make(net.IP, len(start))
	for k := range start {
		end[k] = start[k] | ^n.Mask[k]
	}
	return b.Add(start, end, cc)
}

// compact sorts ranges into starts, fills gaps with "unknown" and merges
// neighbours of the same country. max is the top of the address space.
func compact(rs []rng, max uint64) (starts []uint64, codes []uint8) {
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].start < rs[j].start })
	// starts only grow (gaps begin at next, ranges at or after it), so a new
	// entry is needed only when the country changes
	emit := func(s uint64, c uint8) {
		if n := len(codes); n > 0 && codes[n-1] == c {
			return
		}
		starts, codes = append(starts, s), append(codes, c)
	}
	var next uint64 // first address not covered yet
	full := false
	for _, r := range rs {
		if full {
			break
		}
		if r.start < next {
			if r.end < next {
				continue
			}
			r.start = next
		}
		if r.start > next {
			emit(next, unknown)
		}
		emit(r.start, r.c)
		if r.end == max {
			full = true
		} else {
			next = r.end + 1
		}
	}
	if !full && len(rs) > 0 {
		emit(next, unknown)
	}
	return
}

// Build finishes the data set.
func (b *Builder) Build(source string, built time.Time) *DB {
	db := &DB{Source: source, Built: built, codes: append([]string(nil), b.cc...)}
	s4, c4 := compact(b.r4, math.MaxUint32)
	db.s4, db.c4 = make([]uint32, len(s4)), c4
	for i, s := range s4 {
		db.s4[i] = uint32(s)
	}
	db.s6, db.c6 = compact(b.r6, math.MaxUint64)
	return db
}

// ---------------------------------------------------------------- file format

// Write stores the data as a gzip-compressed DVXGEO1 file.
func (db *DB) Write(w io.Writer) error {
	zw := gzip.NewWriter(w)
	bw := bufio.NewWriter(zw)
	le := func(v any) { binary.Write(bw, binary.BigEndian, v) }
	bw.WriteString(magic)
	le(db.Built.Unix())
	le(uint16(len(db.Source)))
	bw.WriteString(db.Source)
	le(uint16(len(db.codes)))
	for _, c := range db.codes {
		bw.WriteString(c)
	}
	le(uint32(len(db.s4)))
	le(db.s4)
	bw.Write(db.c4)
	le(uint32(len(db.s6)))
	le(db.s6)
	bw.Write(db.c6)
	if err := bw.Flush(); err != nil {
		return err
	}
	return zw.Close()
}

// Read loads a gzip-compressed DVXGEO1 file.
func Read(r io.Reader) (*DB, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	br := bufio.NewReader(io.LimitReader(zr, 64<<20))
	head := make([]byte, len(magic))
	if _, err := io.ReadFull(br, head); err != nil || string(head) != magic {
		return nil, errors.New("not a Dunvex GeoIP file")
	}
	rd := func(v any) error { return binary.Read(br, binary.BigEndian, v) }
	var built int64
	var n16 uint16
	var n32 uint32
	db := &DB{}
	if err := rd(&built); err != nil {
		return nil, err
	}
	db.Built = time.Unix(built, 0).UTC()
	if err := rd(&n16); err != nil {
		return nil, err
	}
	src := make([]byte, n16)
	if _, err := io.ReadFull(br, src); err != nil {
		return nil, err
	}
	db.Source = string(src)
	if err := rd(&n16); err != nil || n16 > maxCode {
		return nil, errors.New("bad country table")
	}
	cc := make([]byte, 2*int(n16))
	if _, err := io.ReadFull(br, cc); err != nil {
		return nil, err
	}
	for i := 0; i < int(n16); i++ {
		db.codes = append(db.codes, string(cc[2*i:2*i+2]))
	}
	if err := rd(&n32); err != nil || n32 > 1<<23 {
		return nil, errors.New("bad IPv4 table")
	}
	db.s4, db.c4 = make([]uint32, n32), make([]uint8, n32)
	if err := rd(db.s4); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(br, db.c4); err != nil {
		return nil, err
	}
	if err := rd(&n32); err != nil || n32 > 1<<23 {
		return nil, errors.New("bad IPv6 table")
	}
	db.s6, db.c6 = make([]uint64, n32), make([]uint8, n32)
	if err := rd(db.s6); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(br, db.c6); err != nil {
		return nil, err
	}
	return db, nil
}

// Open loads a DVXGEO1 file, or an Xray geoip.dat.
func Open(path string) (*DB, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b {
		return Read(bytes.NewReader(data))
	}
	st, _ := os.Stat(path)
	db, err := FromXrayDat(data)
	if err == nil && st != nil {
		db.Built = st.ModTime().UTC()
	}
	return db, err
}

// ---------------------------------------------------------------- loading

// Loader keeps the best data file loaded and picks up a newer one by itself.
// Paths are tried in order (the full country file first, Xray's geoip.dat after).
type Loader struct {
	Paths []string

	mu      sync.Mutex
	db      *DB
	path    string
	mod     time.Time
	checked time.Time
	err     error
}

// Get returns the loaded data, or nil and the reason when there is none.
func (l *Loader) Get() (*DB, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if time.Since(l.checked) < 30*time.Second && (l.db != nil || l.err != nil) {
		return l.db, l.err
	}
	l.checked = time.Now()
	for _, p := range l.Paths {
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			continue
		}
		if l.db != nil && p == l.path && st.ModTime().Equal(l.mod) {
			return l.db, nil
		}
		db, err := Open(p)
		if err != nil {
			continue // a broken file: try the next one
		}
		l.db, l.path, l.mod, l.err = db, p, st.ModTime(), nil
		return db, nil
	}
	l.db, l.err = nil, errors.New("no GeoIP data on this server yet")
	return nil, l.err
}
