package main

import (
	"fmt"
	"strings"

	"rsc.io/qr"
)

// qrLines draws a scannable QR code with half blocks (two modules per line).
func qrLines(text string) []string {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return []string{"(QR: " + err.Error() + ")"}
	}
	const quiet = 2
	n := c.Size
	dark := func(x, y int) bool { return x >= 0 && y >= 0 && x < n && y < n && c.Black(x, y) }
	out := []string{}
	for y := -quiet; y < n+quiet; y += 2 {
		var b strings.Builder
		for x := -quiet; x < n+quiet; x++ {
			fg, bg := 231, 231
			if dark(x, y) {
				fg = 16
			}
			if dark(x, y+1) {
				bg = 16
			}
			fmt.Fprintf(&b, "\x1b[38;5;%d;48;5;%dm▀", fg, bg)
		}
		b.WriteString("\x1b[0m")
		out = append(out, b.String())
	}
	return out
}

func printQR(text, indent string) error {
	for _, l := range qrLines(text) {
		fmt.Println(indent + l)
	}
	return nil
}
