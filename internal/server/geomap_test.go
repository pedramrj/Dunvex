package server

import (
	"net"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/geoip"
)

func TestGroupClients(t *testing.T) {
	b := geoip.NewBuilder()
	b.Add(net.ParseIP("2.144.0.0"), net.ParseIP("2.147.255.255"), "IR")
	b.Add(net.ParseIP("91.107.0.0"), net.ParseIP("91.107.255.255"), "DE")
	db := b.Build("test data", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	seen := []engine.Seen{
		{User: "sara", IP: "2.144.1.1"},
		{User: "sara", IP: "2.145.9.9"}, // second device of sara, same country
		{User: "reza", IP: "2.146.0.5"},
		{User: "ali", IP: "91.107.1.1"},
		{User: "ali", IP: "192.168.1.20"}, // private: no country
		{User: "mina", IP: "8.8.8.8"},     // not in this data
	}
	out := groupClients(seen, db)
	if out.Devices != 6 || out.Users != 4 || out.Unplaced != 2 || len(out.Countries) != 2 {
		t.Fatalf("totals: %+v", out)
	}
	ir := out.Countries[0]
	if ir.CC != "IR" || ir.Devices != 3 || ir.Users != 2 || ir.Lat == 0 {
		t.Fatalf("Iran first with 3 devices of 2 users: %+v", ir)
	}
	if out.Countries[1].CC != "DE" || out.Source != "test data" || out.Built == 0 {
		t.Fatalf("Germany and source: %+v", out)
	}

	// without data every device is counted but none placed
	none := groupClients(seen, nil)
	if none.Unplaced != 6 || len(none.Countries) != 0 {
		t.Fatalf("no data: %+v", none)
	}
}
