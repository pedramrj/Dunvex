package geodata

import (
	"encoding/binary"
	"net"
	"os"
	"sort"
)

// WriteSite writes a geosite.dat with the given lists (used by tests).
func WriteSite(path string, lists map[string][]Domain) error {
	var out []byte
	for _, code := range sortedKeys(lists) {
		var e []byte
		e = putBytes(e, 1, []byte(code))
		for _, d := range lists[code] {
			var dm []byte
			dm = putVarint(dm, 1, uint64(d.Kind))
			dm = putBytes(dm, 2, []byte(d.Value))
			e = putBytes(e, 2, dm)
		}
		out = putBytes(out, 1, e)
	}
	return os.WriteFile(path, out, 0o644)
}

// WriteIP writes a geoip.dat with the given lists (used by tests).
func WriteIP(path string, lists map[string][]string) error {
	var out []byte
	for _, code := range sortedKeys(lists) {
		var e []byte
		e = putBytes(e, 1, []byte(code))
		for _, c := range lists[code] {
			_, n, err := net.ParseCIDR(c)
			if err != nil {
				return err
			}
			ip := n.IP.To4()
			if ip == nil {
				ip = n.IP.To16()
			}
			ones, _ := n.Mask.Size()
			var cm []byte
			cm = putBytes(cm, 1, ip)
			cm = putVarint(cm, 2, uint64(ones))
			e = putBytes(e, 2, cm)
		}
		out = putBytes(out, 1, e)
	}
	return os.WriteFile(path, out, 0o644)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func putVarint(b []byte, num int, v uint64) []byte {
	b = binary.AppendUvarint(b, uint64(num)<<3)
	return binary.AppendUvarint(b, v)
}

func putBytes(b []byte, num int, v []byte) []byte {
	b = binary.AppendUvarint(b, uint64(num)<<3|2)
	b = binary.AppendUvarint(b, uint64(len(v)))
	return append(b, v...)
}
