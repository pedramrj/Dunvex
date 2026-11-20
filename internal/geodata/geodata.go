// Package geodata reads Xray's geosite.dat and geoip.dat (the v2ray
// "routercommon" protobuf lists) so the same site and country lists can drive
// sing-box rule-sets and client subscriptions.
package geodata

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
)

// Domain kinds in geosite.dat.
const (
	Keyword = 0 // plain: the text appears anywhere in the name
	Regex   = 1
	Suffix  = 2 // the domain and its subdomains
	Full    = 3
)

// Domain is one entry of a site list.
type Domain struct {
	Kind  int
	Value string
}

// Site lists the domains of one geosite code (attributes are ignored).
func Site(path, code string) ([]Domain, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	entry, err := find(data, code)
	if err != nil {
		return nil, err
	}
	var out []Domain
	err = fields(entry, func(num int, wire int, v uint64, b []byte) error {
		if num != 2 || wire != 2 {
			return nil
		}
		var d Domain
		if err := fields(b, func(n, w int, v uint64, b []byte) error {
			switch {
			case n == 1 && w == 0:
				d.Kind = int(v)
			case n == 2 && w == 2:
				d.Value = string(b)
			}
			return nil
		}); err != nil {
			return err
		}
		if d.Value != "" {
			out = append(out, d)
		}
		return nil
	})
	return out, err
}

// IP lists the networks of one geoip code.
func IP(path, code string) ([]*net.IPNet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	entry, err := find(data, code)
	if err != nil {
		return nil, err
	}
	var out []*net.IPNet
	err = fields(entry, func(num int, wire int, v uint64, b []byte) error {
		if num != 2 || wire != 2 {
			return nil
		}
		var ip []byte
		var prefix uint64
		if err := fields(b, func(n, w int, v uint64, b []byte) error {
			switch {
			case n == 1 && w == 2:
				ip = b
			case n == 2 && w == 0:
				prefix = v
			}
			return nil
		}); err != nil {
			return err
		}
		if (len(ip) == 4 && prefix <= 32) || (len(ip) == 16 && prefix <= 128) {
			out = append(out, &net.IPNet{IP: net.IP(append([]byte(nil), ip...)), Mask: net.CIDRMask(int(prefix), len(ip)*8)})
		}
		return nil
	})
	return out, err
}

// Codes lists every code in a .dat file, sorted.
func Codes(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	err = fields(data, func(num, wire int, v uint64, b []byte) error {
		if num == 1 && wire == 2 {
			if c := code(b); c != "" {
				out = append(out, strings.ToLower(c))
			}
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// ErrNoCode means the file has no list with that name.
var ErrNoCode = errors.New("not in the file")

// find returns the entry (GeoSite or GeoIP message) whose code matches.
func find(data []byte, want string) ([]byte, error) {
	var hit []byte
	err := fields(data, func(num, wire int, v uint64, b []byte) error {
		if hit == nil && num == 1 && wire == 2 && strings.EqualFold(code(b), want) {
			hit = b
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if hit == nil {
		return nil, fmt.Errorf("%s: %w", want, ErrNoCode)
	}
	return hit, nil
}

// code is field 1 (country_code) of an entry.
func code(entry []byte) string {
	var c string
	fields(entry, func(num, wire int, v uint64, b []byte) error {
		if num == 1 && wire == 2 && c == "" {
			c = string(b)
		}
		return nil
	})
	return c
}

// fields walks one protobuf message, calling fn for each field.
func fields(msg []byte, fn func(num, wire int, v uint64, b []byte) error) error {
	for len(msg) > 0 {
		key, n := binary.Uvarint(msg)
		if n <= 0 {
			return errors.New("geodata: bad field key")
		}
		msg = msg[n:]
		num, wire := int(key>>3), int(key&7)
		switch wire {
		case 0:
			v, n := binary.Uvarint(msg)
			if n <= 0 {
				return errors.New("geodata: bad varint")
			}
			msg = msg[n:]
			if err := fn(num, wire, v, nil); err != nil {
				return err
			}
		case 1:
			if len(msg) < 8 {
				return errors.New("geodata: short fixed64")
			}
			msg = msg[8:]
		case 2:
			l, n := binary.Uvarint(msg)
			if n <= 0 || uint64(len(msg)-n) < l {
				return errors.New("geodata: bad length")
			}
			b := msg[n : n+int(l)]
			msg = msg[n+int(l):]
			if err := fn(num, wire, 0, b); err != nil {
				return err
			}
		case 5:
			if len(msg) < 4 {
				return errors.New("geodata: short fixed32")
			}
			msg = msg[4:]
		default:
			return fmt.Errorf("geodata: wire type %d", wire)
		}
	}
	return nil
}
