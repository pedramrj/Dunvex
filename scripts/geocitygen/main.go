// Command geocitygen builds geoip-city.bin.gz, the offline city and network
// data the panel's world map and user pages use, from DB-IP's free "IP to City
// Lite" and "IP to ASN Lite" databases (https://db-ip.com, CC BY 4.0). Release
// builds run it; the file is published next to the binaries and servers
// download it at install, like the country data.
//
//	go run ./scripts/geocitygen -out dist/geoip-city.bin.gz
//	go run ./scripts/geocitygen -city dbip-city-lite-2026-11.csv.gz -asn dbip-asn-lite-2026-11.csv.gz -out geoip-city.bin.gz
package main

import (
	"bufio"
	"compress/gzip"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pedramrj/dunvex/internal/geoip"
)

func main() {
	city := flag.String("city", "", "a local dbip-city-lite-YYYY-MM.csv(.gz); downloaded when empty")
	asn := flag.String("asn", "", "a local dbip-asn-lite-YYYY-MM.csv(.gz); downloaded when empty")
	out := flag.String("out", "geoip-city.bin.gz", "output file")
	flag.Parse()

	b := geoip.NewCityBuilder()
	cr, cmonth, err := open(*city, "city")
	if err != nil {
		log.Fatal(err)
	}
	places, err := read(cr, 8, func(rec []string, s, e net.IP) error {
		lat, _ := strconv.ParseFloat(rec[6], 64)
		lon, _ := strconv.ParseFloat(rec[7], 64)
		cc := strings.ToUpper(rec[3])
		if cc == "ZZ" || (lat == 0 && lon == 0) {
			return nil
		}
		return b.AddPlace(s, e, geoip.Place{CC: cc, Region: rec[4], City: rec[5], Lat: lat, Lon: lon})
	})
	cr.Close()
	if err != nil {
		log.Fatal(err)
	}
	ar, _, err := open(*asn, "asn")
	if err != nil {
		log.Fatal(err)
	}
	nets, err := read(ar, 4, func(rec []string, s, e net.IP) error {
		n, err := strconv.ParseUint(rec[2], 10, 32)
		if err != nil || n == 0 {
			return nil
		}
		return b.AddNetwork(s, e, geoip.Network{ASN: uint32(n), Org: rec[3]})
	})
	ar.Close()
	if err != nil {
		log.Fatal(err)
	}
	built := time.Now()
	if t, err := time.Parse("2006-01", cmonth); err == nil {
		built = t
	}
	db := b.Build("DB-IP IP to City Lite and ASN Lite "+cmonth+" (db-ip.com, CC BY 4.0)", built)
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	if err := db.Write(f); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
	p, r4, r6, n := db.Counts()
	st, _ := os.Stat(*out)
	fmt.Printf("%s: %d place rows, %d network rows read; %d places, %d+%d ranges, %d networks; %d bytes\n", *out, places, nets, p, r4, r6, n, st.Size())
}

// open returns the CSV (gunzipped) and its month, downloading this or last
// month's file when no local one is given.
func open(path, kind string) (io.ReadCloser, string, error) {
	prefix := "dbip-" + kind + "-lite-"
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, "", err
		}
		month := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(baseName(path), prefix), ".gz"), ".csv")
		if strings.HasSuffix(path, ".gz") {
			zr, err := gzip.NewReader(f)
			if err != nil {
				f.Close()
				return nil, "", err
			}
			return readCloser{zr, f}, month, nil
		}
		return f, month, nil
	}
	now := time.Now().UTC()
	for _, t := range []time.Time{now, now.AddDate(0, -1, 0)} {
		m := t.Format("2006-01")
		res, err := (&http.Client{Timeout: 15 * time.Minute}).Get("https://download.db-ip.com/free/" + prefix + m + ".csv.gz")
		if err != nil {
			continue
		}
		if res.StatusCode != 200 {
			res.Body.Close()
			continue
		}
		zr, err := gzip.NewReader(res.Body)
		if err != nil {
			res.Body.Close()
			continue
		}
		return readCloser{zr, res.Body}, m, nil
	}
	return nil, "", errors.New("could not download the DB-IP " + kind + " lite file")
}

type readCloser struct {
	io.Reader
	c io.Closer
}

func (r readCloser) Close() error { return r.c.Close() }

func read(r io.Reader, fields int, add func(rec []string, start, end net.IP) error) (int, error) {
	cr := csv.NewReader(bufio.NewReaderSize(r, 1<<20))
	cr.FieldsPerRecord = fields
	cr.ReuseRecord = true
	rows := 0
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			return rows, nil
		}
		if err != nil {
			return rows, err
		}
		s, e := net.ParseIP(rec[0]), net.ParseIP(rec[1])
		if s == nil || e == nil {
			continue
		}
		if err := add(rec, s, e); err != nil {
			return rows, err
		}
		rows++
	}
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}
