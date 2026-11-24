package geoip

// City data: where an address is (city, region, country and coordinates) and
// which network it belongs to (AS number and operator). It is a separate,
// larger file than the country data (geoip-city.bin.gz, made by
// scripts/geocitygen from DB-IP's free "IP to City Lite" and "IP to ASN Lite"
// databases, CC BY 4.0) and is optional: without it the map falls back to
// country centres. Lookups never leave the server.

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
	"sync"
	"time"
)

const (
	cityMagic = "DVXCITY1\n"
	none      = ^uint32(0)
)

// Place is where an address is.
type Place struct {
	City   string  `json:"city,omitempty"`
	Region string  `json:"region,omitempty"`
	CC     string  `json:"cc"`
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
}

// Network is who an address belongs to.
type Network struct {
	ASN uint32 `json:"asn"`
	Org string `json:"org"`
}

// CityDB answers place and network lookups from sorted range starts.
type CityDB struct {
	Source string
	Built  time.Time

	places []Place
	s4     []uint32
	p4     []uint32
	s6     []uint64 // first 64 bits of the address
	p6     []uint32

	nets []Network
	n4s  []uint32
	n4   []uint32
	n6s  []uint64
	n6   []uint32
}

func find(ip net.IP, s4 []uint32, v4 []uint32, s6 []uint64, v6 []uint32) uint32 {
	if ip == nil {
		return none
	}
	if a4 := ip.To4(); a4 != nil {
		a := binary.BigEndian.Uint32(a4)
		i := sort.Search(len(s4), func(k int) bool { return s4[k] > a }) - 1
		if i < 0 || i >= len(v4) {
			return none
		}
		return v4[i]
	}
	if a16 := ip.To16(); a16 != nil {
		a := binary.BigEndian.Uint64(a16[:8])
		i := sort.Search(len(s6), func(k int) bool { return s6[k] > a }) - 1
		if i < 0 || i >= len(v6) {
			return none
		}
		return v6[i]
	}
	return none
}

// Place returns where ip is, when known.
func (db *CityDB) Place(ip net.IP) (Place, bool) {
	if db == nil {
		return Place{}, false
	}
	i := find(ip, db.s4, db.p4, db.s6, db.p6)
	if i == none || int(i) >= len(db.places) {
		return Place{}, false
	}
	return db.places[i], true
}

// Network returns the AS number and operator of ip, when known.
func (db *CityDB) Network(ip net.IP) (Network, bool) {
	if db == nil {
		return Network{}, false
	}
	i := find(ip, db.n4s, db.n4, db.n6s, db.n6)
	if i == none || int(i) >= len(db.nets) {
		return Network{}, false
	}
	return db.nets[i], true
}

// Counts reports the sizes of the tables, for the settings page.
func (db *CityDB) Counts() (places, v4, v6, networks int) {
	return len(db.places), len(db.s4), len(db.s6), len(db.nets)
}

// ---------------------------------------------------------------- building

type r32 struct {
	start, end uint32
	v          uint32
}

type r64 struct {
	start, end uint64
	v          uint32
}

// CityBuilder collects ranges before they are packed.
type CityBuilder struct {
	pidx   map[[3]string]uint32 // one place per country, region and city
	places []Place
	p4     []r32
	p6     []r64
	nidx   map[Network]uint32
	nets   []Network
	n4     []r32
	n6     []r64
}

// NewCityBuilder returns an empty builder.
func NewCityBuilder() *CityBuilder {
	return &CityBuilder{pidx: map[[3]string]uint32{}, nidx: map[Network]uint32{}}
}

func round(v float64, step float64) float64 { return math.Round(v/step) * step }

func (b *CityBuilder) add(start, end net.IP, v uint32, r4 *[]r32, r6 *[]r64) error {
	if s4, e4 := start.To4(), end.To4(); s4 != nil && e4 != nil {
		*r4 = append(*r4, r32{binary.BigEndian.Uint32(s4), binary.BigEndian.Uint32(e4), v})
		return nil
	}
	s16, e16 := start.To16(), end.To16()
	if s16 == nil || e16 == nil || start.To4() != nil || end.To4() != nil {
		return fmt.Errorf("bad range %s-%s", start, end)
	}
	*r6 = append(*r6, r64{binary.BigEndian.Uint64(s16[:8]), binary.BigEndian.Uint64(e16[:8]), v})
	return nil
}

// AddPlace records that start..end is at p. A city is one place (its first
// coordinates, kept to about a kilometre): the source repeats a city with
// slightly different points, and one place lets neighbouring ranges merge.
func (b *CityBuilder) AddPlace(start, end net.IP, p Place) error {
	p.Lat, p.Lon = round(p.Lat, 0.01), round(p.Lon, 0.01)
	key := [3]string{p.CC, p.Region, p.City}
	i, ok := b.pidx[key]
	if !ok {
		i = uint32(len(b.places))
		b.pidx[key] = i
		b.places = append(b.places, p)
	}
	return b.add(start, end, i, &b.p4, &b.p6)
}

// AddNetwork records that start..end belongs to n.
func (b *CityBuilder) AddNetwork(start, end net.IP, n Network) error {
	i, ok := b.nidx[n]
	if !ok {
		i = uint32(len(b.nets))
		b.nidx[n] = i
		b.nets = append(b.nets, n)
	}
	return b.add(start, end, i, &b.n4, &b.n6)
}

// pack turns ranges into starts and values, filling gaps with none and joining
// neighbours with the same value.
func pack32(rs []r32) (starts, vals []uint32) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].start < rs[j].start })
	var next uint64
	push := func(s uint32, v uint32) {
		if n := len(vals); n > 0 && vals[n-1] == v {
			return
		}
		if n := len(starts); n > 0 && starts[n-1] == s {
			vals[n-1] = v
			return
		}
		starts, vals = append(starts, s), append(vals, v)
	}
	for _, r := range rs {
		if uint64(r.start) < next {
			if uint64(r.end) < next {
				continue
			}
			r.start = uint32(next)
		}
		if uint64(r.start) > next {
			push(uint32(next), none)
		}
		push(r.start, r.v)
		next = uint64(r.end) + 1
	}
	if next <= math.MaxUint32 && len(starts) > 0 {
		push(uint32(next), none)
	}
	return
}

func pack64(rs []r64) (starts []uint64, vals []uint32) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].start < rs[j].start })
	var next uint64
	done := false
	push := func(s uint64, v uint32) {
		if n := len(vals); n > 0 && vals[n-1] == v {
			return
		}
		if n := len(starts); n > 0 && starts[n-1] == s {
			vals[n-1] = v
			return
		}
		starts, vals = append(starts, s), append(vals, v)
	}
	for _, r := range rs {
		if done {
			break
		}
		if r.start < next {
			if r.end < next {
				continue
			}
			r.start = next
		}
		if r.start > next {
			push(next, none)
		}
		push(r.start, r.v)
		if r.end == math.MaxUint64 {
			done = true
		}
		next = r.end + 1
	}
	if !done && len(starts) > 0 {
		push(next, none)
	}
	return
}

// coarsen keeps one entry per block of 2^shift addresses (the first one in
// it), so the tables stay small enough for a small server's memory: an
// IPv4 block of 1024 and a /36 for IPv6 rarely spans two cities.
func coarsen[T uint32 | uint64](starts []T, vals []uint32, shift uint) ([]T, []uint32) {
	outS, outV := starts[:0:0], vals[:0:0]
	for i, s := range starts {
		r := s >> shift << shift
		n := len(outS)
		if n > 0 && outS[n-1] == r {
			if outV[n-1] == none {
				outV[n-1] = vals[i]
			}
			continue
		}
		if n > 0 && outV[n-1] == vals[i] {
			continue
		}
		outS, outV = append(outS, r), append(outV, vals[i])
	}
	return outS, outV
}

// Build packs the collected ranges.
func (b *CityBuilder) Build(source string, built time.Time) *CityDB {
	db := &CityDB{Source: source, Built: built.UTC(), places: b.places, nets: b.nets}
	s4, v4 := pack32(b.p4)
	db.s4, db.p4 = coarsen(s4, v4, 10)
	s6, v6 := pack64(b.p6)
	db.s6, db.p6 = coarsen(s6, v6, 28)
	n4s, n4 := pack32(b.n4)
	db.n4s, db.n4 = coarsen(n4s, n4, 8)
	n6s, n6 := pack64(b.n6)
	db.n6s, db.n6 = coarsen(n6s, n6, 24)
	db.places = keepUsed(db.places, db.p4, db.p6)
	db.nets = keepUsed(db.nets, db.n4, db.n6)
	return db
}

// keepUsed drops table rows no range points at any more and renumbers.
func keepUsed[T any](rows []T, refs ...[]uint32) []T {
	idx := make([]uint32, len(rows))
	for i := range idx {
		idx[i] = none
	}
	var out []T
	for _, r := range refs {
		for i, v := range r {
			if v == none || int(v) >= len(rows) {
				continue
			}
			if idx[v] == none {
				idx[v] = uint32(len(out))
				out = append(out, rows[v])
			}
			r[i] = idx[v]
		}
	}
	return out
}

// ---------------------------------------------------------------- file format

// Write stores the data as a gzip-compressed DVXCITY1 file.
func (db *CityDB) Write(w io.Writer) error {
	zw, _ := gzip.NewWriterLevel(w, gzip.BestCompression)
	bw := bufio.NewWriter(zw)
	be := func(v any) { binary.Write(bw, binary.BigEndian, v) }
	str := func(s string) {
		if len(s) > math.MaxUint16 {
			s = s[:math.MaxUint16]
		}
		be(uint16(len(s)))
		bw.WriteString(s)
	}
	bw.WriteString(cityMagic)
	be(db.Built.Unix())
	str(db.Source)
	be(uint32(len(db.places)))
	for _, p := range db.places {
		cc := p.CC
		if len(cc) != 2 {
			cc = "--"
		}
		bw.WriteString(cc)
		be(int32(math.Round(p.Lat * 1e4)))
		be(int32(math.Round(p.Lon * 1e4)))
		str(p.City)
		str(p.Region)
	}
	be(uint32(len(db.nets)))
	for _, n := range db.nets {
		be(n.ASN)
		str(n.Org)
	}
	table32 := func(s, v []uint32) { be(uint32(len(s))); be(s); be(v) }
	table64 := func(s []uint64, v []uint32) { be(uint32(len(s))); be(s); be(v) }
	table32(db.s4, db.p4)
	table64(db.s6, db.p6)
	table32(db.n4s, db.n4)
	table64(db.n6s, db.n6)
	if err := bw.Flush(); err != nil {
		return err
	}
	return zw.Close()
}

// ReadCity loads a gzip-compressed DVXCITY1 file.
func ReadCity(r io.Reader) (*CityDB, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	br := bufio.NewReaderSize(io.LimitReader(zr, 512<<20), 1<<16)
	head := make([]byte, len(cityMagic))
	if _, err := io.ReadFull(br, head); err != nil || string(head) != cityMagic {
		return nil, errors.New("not a Dunvex city file")
	}
	var ferr error
	rd := func(v any) {
		if ferr == nil {
			ferr = binary.Read(br, binary.BigEndian, v)
		}
	}
	str := func() string {
		var n uint16
		rd(&n)
		if ferr != nil {
			return ""
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(br, b); err != nil {
			ferr = err
		}
		return string(b)
	}
	count := func(max uint32) int {
		var n uint32
		rd(&n)
		if ferr == nil && n > max {
			ferr = errors.New("bad table size")
		}
		return int(n)
	}
	db := &CityDB{}
	var built int64
	rd(&built)
	db.Built = time.Unix(built, 0).UTC()
	db.Source = str()
	np := count(1 << 24)
	db.places = make([]Place, 0, np)
	for i := 0; i < np && ferr == nil; i++ {
		cc := make([]byte, 2)
		if _, err := io.ReadFull(br, cc); err != nil {
			ferr = err
			break
		}
		var lat, lon int32
		rd(&lat)
		rd(&lon)
		p := Place{CC: string(cc), Lat: float64(lat) / 1e4, Lon: float64(lon) / 1e4}
		p.City = str()
		p.Region = str()
		if p.CC == "--" {
			p.CC = ""
		}
		db.places = append(db.places, p)
	}
	nn := count(1 << 22)
	db.nets = make([]Network, 0, nn)
	for i := 0; i < nn && ferr == nil; i++ {
		var n Network
		rd(&n.ASN)
		n.Org = str()
		db.nets = append(db.nets, n)
	}
	t32 := func() ([]uint32, []uint32) {
		n := count(1 << 26)
		if ferr != nil {
			return nil, nil
		}
		s, v := make([]uint32, n), make([]uint32, n)
		rd(s)
		rd(v)
		return s, v
	}
	t64 := func() ([]uint64, []uint32) {
		n := count(1 << 26)
		if ferr != nil {
			return nil, nil
		}
		s, v := make([]uint64, n), make([]uint32, n)
		rd(s)
		rd(v)
		return s, v
	}
	db.s4, db.p4 = t32()
	db.s6, db.p6 = t64()
	db.n4s, db.n4 = t32()
	db.n6s, db.n6 = t64()
	if ferr != nil {
		return nil, fmt.Errorf("city file: %w", ferr)
	}
	return db, nil
}

// ---------------------------------------------------------------- loading

// CityLoader keeps the city file loaded and picks up a newer one by itself.
// The file is large, so it is read only when something first asks for it.
type CityLoader struct {
	Path string

	mu      sync.Mutex
	db      *CityDB
	mod     time.Time
	checked time.Time
	err     error
}

// Get returns the loaded data, or nil and the reason when there is none.
func (l *CityLoader) Get() (*CityDB, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if time.Since(l.checked) < time.Minute && (l.db != nil || l.err != nil) {
		return l.db, l.err
	}
	l.checked = time.Now()
	st, err := os.Stat(l.Path)
	if err != nil || st.IsDir() {
		l.db, l.err = nil, errors.New("no city data on this server yet")
		return nil, l.err
	}
	if l.db != nil && st.ModTime().Equal(l.mod) {
		return l.db, nil
	}
	data, err := os.ReadFile(l.Path)
	if err != nil {
		l.err = err
		return l.db, err
	}
	db, err := ReadCity(bytes.NewReader(data))
	if err != nil {
		l.err = err
		return l.db, err
	}
	l.db, l.mod, l.err = db, st.ModTime(), nil
	return db, nil
}
