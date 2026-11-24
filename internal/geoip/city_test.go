package geoip

import (
	"bytes"
	"net"
	"testing"
	"time"
)

func TestCityRoundTrip(t *testing.T) {
	b := NewCityBuilder()
	ip := net.ParseIP
	tehran := Place{CC: "IR", Region: "Tehran", City: "Tehran", Lat: 35.6892, Lon: 51.389}
	karaj := Place{CC: "IR", Region: "Alborz", City: "Karaj", Lat: 35.84, Lon: 50.94}
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(b.AddPlace(ip("5.0.0.0"), ip("5.0.255.255"), tehran))
	must(b.AddPlace(ip("5.1.0.0"), ip("5.1.255.255"), karaj))
	must(b.AddPlace(ip("2a01:5ec0::"), ip("2a01:5ec0:ffff:ffff:ffff:ffff:ffff:ffff"), tehran))
	must(b.AddNetwork(ip("5.0.0.0"), ip("5.1.255.255"), Network{ASN: 44244, Org: "Iran Cell Service and Communication Company"}))
	db := b.Build("test", time.Unix(1700000000, 0))
	var buf bytes.Buffer
	must(db.Write(&buf))
	got, err := ReadCity(&buf)
	must(err)
	for _, c := range []struct {
		ip   string
		city string
		ok   bool
	}{{"5.0.3.4", "Tehran", true}, {"5.1.200.1", "Karaj", true}, {"5.2.0.1", "", false}, {"4.255.255.255", "", false}, {"2a01:5ec0:12::1", "Tehran", true}} {
		p, ok := got.Place(ip(c.ip))
		if ok != c.ok || p.City != c.city {
			t.Errorf("%s: got %q %v, want %q %v", c.ip, p.City, ok, c.city, c.ok)
		}
	}
	if n, ok := got.Network(ip("5.1.2.3")); !ok || n.ASN != 44244 {
		t.Errorf("network: %+v %v", n, ok)
	}
	if _, ok := got.Network(ip("6.0.0.1")); ok {
		t.Error("network outside any range")
	}
	if p, _ := got.Place(ip("5.0.0.1")); p.Region != "Tehran" || p.CC != "IR" || p.Lat < 35.68 || p.Lat > 35.7 {
		t.Errorf("place fields: %+v", p)
	}
}
