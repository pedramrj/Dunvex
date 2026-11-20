package geoip

import (
	"errors"
	"net"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
)

// FromXrayDat reads the country lists of an Xray / v2fly geoip.dat
// (GeoIPList → GeoIP{country_code, cidr[]} → CIDR{ip, prefix}). Only
// two-letter country entries are used; lists such as "private" or "telegram"
// are not countries.
func FromXrayDat(data []byte) (*DB, error) {
	b := NewBuilder()
	found := 0
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return nil, errors.New("bad geoip.dat")
		}
		data = data[n:]
		if num != 1 || typ != protowire.BytesType {
			if n = protowire.ConsumeFieldValue(num, typ, data); n < 0 {
				return nil, errors.New("bad geoip.dat")
			}
			data = data[n:]
			continue
		}
		entry, n := protowire.ConsumeBytes(data)
		if n < 0 {
			return nil, errors.New("bad geoip.dat")
		}
		data = data[n:]
		cc, nets, inverse, err := readGeoIP(entry)
		if err != nil {
			return nil, err
		}
		if len(cc) != 2 || inverse {
			continue
		}
		found++
		for _, ipn := range nets {
			b.AddCIDR(ipn, cc)
		}
	}
	if found == 0 {
		return nil, errors.New("geoip.dat has no country lists")
	}
	return b.Build("Xray geoip.dat (fewer countries)", time.Time{}), nil
}

func readGeoIP(b []byte) (cc string, nets []*net.IPNet, inverse bool, err error) {
	bad := errors.New("bad geoip.dat entry")
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return "", nil, false, bad
		}
		b = b[n:]
		switch {
		case num == 1 && typ == protowire.BytesType:
			v, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return "", nil, false, bad
			}
			cc, b = string(v), b[n:]
		case num == 2 && typ == protowire.BytesType:
			v, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return "", nil, false, bad
			}
			b = b[n:]
			if ipn := readCIDR(v); ipn != nil {
				nets = append(nets, ipn)
			}
		case num == 3 && typ == protowire.VarintType:
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return "", nil, false, bad
			}
			inverse, b = v != 0, b[n:]
		default:
			n := protowire.ConsumeFieldValue(num, typ, b)
			if n < 0 {
				return "", nil, false, bad
			}
			b = b[n:]
		}
	}
	return
}

func readCIDR(b []byte) *net.IPNet {
	var ip []byte
	var prefix uint64
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return nil
		}
		b = b[n:]
		switch {
		case num == 1 && typ == protowire.BytesType:
			v, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return nil
			}
			ip, b = v, b[n:]
		case num == 2 && typ == protowire.VarintType:
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return nil
			}
			prefix, b = v, b[n:]
		default:
			n := protowire.ConsumeFieldValue(num, typ, b)
			if n < 0 {
				return nil
			}
			b = b[n:]
		}
	}
	bits := len(ip) * 8
	if (len(ip) != 4 && len(ip) != 16) || int(prefix) > bits {
		return nil
	}
	return &net.IPNet{IP: net.IP(ip), Mask: net.CIDRMask(int(prefix), bits)}
}
