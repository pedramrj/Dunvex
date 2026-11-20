// Command geoipgen builds geoip-country.bin.gz, the offline country data the
// network map uses, from DB-IP's free "IP to Country Lite" database
// (https://db-ip.com, CC BY 4.0). Release builds run it; the file is published
// next to the binaries and servers download it with a checksum.
//
//	go run ./scripts/geoipgen -out dist/geoip-country.bin.gz
//	go run ./scripts/geoipgen -in dbip-country-lite-2026-11.csv.gz -out geoip-country.bin.gz
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
	"strings"
	"time"

	"github.com/pedramrj/dunvex/internal/geoip"
)

func main() {
	in := flag.String("in", "", "a local dbip-country-lite-YYYY-MM.csv(.gz); downloaded when empty")
	out := flag.String("out", "geoip-country.bin.gz", "output file")
	flag.Parse()

	var r io.Reader
	month := ""
	if *in != "" {
		f, err := os.Open(*in)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		r = f
		month = strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(baseName(*in), "dbip-country-lite-"), ".gz"), ".csv")
	} else {
		body, m, err := fetch()
		if err != nil {
			log.Fatal(err)
		}
		defer body.Close()
		r, month = body, m
	}
	db, rows, err := build(r, month)
	if err != nil {
		log.Fatal(err)
	}
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
	v4, v6 := db.Ranges()
	st, _ := os.Stat(*out)
	fmt.Printf("%s: %d rows → %d countries, %d IPv4 + %d IPv6 ranges, %d bytes (%s)\n", *out, rows, db.Countries(), v4, v6, st.Size(), db.Source)
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// fetch downloads this month's file, or last month's early in a month.
func fetch() (io.ReadCloser, string, error) {
	now := time.Now().UTC()
	var errs []string
	for _, t := range []time.Time{now, now.AddDate(0, -1, 0)} {
		m := t.Format("2006-01")
		url := "https://download.db-ip.com/free/dbip-country-lite-" + m + ".csv.gz"
		res, err := (&http.Client{Timeout: 5 * time.Minute}).Get(url)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		if res.StatusCode != 200 {
			res.Body.Close()
			errs = append(errs, url+": "+res.Status)
			continue
		}
		return res.Body, m, nil
	}
	return nil, "", errors.New("download failed: " + strings.Join(errs, "; "))
}

// build reads "start,end,country" rows (gzip or plain).
func build(r io.Reader, month string) (*geoip.DB, int, error) {
	br := bufio.NewReader(r)
	if head, _ := br.Peek(2); len(head) == 2 && head[0] == 0x1f && head[1] == 0x8b {
		zr, err := gzip.NewReader(br)
		if err != nil {
			return nil, 0, err
		}
		defer zr.Close()
		br = bufio.NewReader(zr)
	}
	cr := csv.NewReader(br)
	cr.FieldsPerRecord = -1
	b := geoip.NewBuilder()
	rows := 0
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, rows, err
		}
		if len(rec) < 3 {
			continue
		}
		s, e := net.ParseIP(rec[0]), net.ParseIP(rec[1])
		if s == nil || e == nil {
			continue
		}
		if err := b.Add(s, e, rec[2]); err != nil {
			return nil, rows, err
		}
		rows++
	}
	if rows < 1000 {
		return nil, rows, fmt.Errorf("only %d rows: not a DB-IP country file", rows)
	}
	built := time.Now().UTC()
	if t, err := time.Parse("2006-01", month); err == nil {
		built = t
	}
	return b.Build("DB-IP Country Lite "+month+" · db-ip.com · CC BY 4.0", built), rows, nil
}
