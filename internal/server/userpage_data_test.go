package server

import (
	"testing"
	"time"
)

func TestJalali(t *testing.T) {
	for _, c := range []struct {
		g       time.Time
		y, m, d int
	}{
		{time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC), 1405, 9, 3},
		{time.Date(2025, 3, 21, 12, 0, 0, 0, time.UTC), 1404, 1, 1},
		{time.Date(2024, 3, 19, 12, 0, 0, 0, time.UTC), 1402, 12, 29},
		{time.Date(2024, 3, 20, 12, 0, 0, 0, time.UTC), 1403, 1, 1},
	} {
		y, m, d := jalali(c.g)
		if y != c.y || m != c.m || d != c.d {
			t.Errorf("%s: got %d/%d/%d, want %d/%d/%d", c.g.Format("2006-01-02"), y, m, d, c.y, c.m, c.d)
		}
	}
	if got := faDate(time.Date(2026, 12, 19, 12, 0, 0, 0, time.UTC)); got != "۲۸ آذر ۱۴۰۵" {
		t.Errorf("faDate: %q", got)
	}
	if got := faBytes(28346784153); got != "۲۶٫۴ گیگ" {
		t.Errorf("faBytes: %q", got)
	}
	if got := maskIP("5.120.33.7"); got != "5.120.×.×" {
		t.Errorf("maskIP: %q", got)
	}
}
