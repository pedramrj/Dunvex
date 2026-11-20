package main

import (
	"fmt"
	"strings"

	"rsc.io/qr"
)

// printQR draws a scannable QR code with half blocks (two modules per line).
func printQR(text, indent string) error {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return err
	}
	const quiet = 2
	n := c.Size
	dark := func(x, y int) bool { return x >= 0 && y >= 0 && x < n && y < n && c.Black(x, y) }
	for y := -quiet; y < n+quiet; y += 2 {
		var b strings.Builder
		b.WriteString(indent)
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
		fmt.Println(b.String())
	}
	return nil
}
