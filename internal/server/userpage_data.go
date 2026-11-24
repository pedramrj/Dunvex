package server

// Extra facts for the user's own page: dates in the Persian calendar with
// Persian digits, the last week of use, when the data runs out at that pace,
// and the devices this user is connected from right now (their city, network
// and server; the address is shortened). Only this user's own data is shown.

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/pedramrj/dunvex/internal/store"
)

var faMonths = []string{"فروردین", "اردیبهشت", "خرداد", "تیر", "مرداد", "شهریور", "مهر", "آبان", "آذر", "دی", "بهمن", "اسفند"}

// jalali converts a Gregorian date to the Persian (Solar Hijri) calendar.
func jalali(t time.Time) (jy, jm, jd int) {
	gy, gm, gd := t.Year(), int(t.Month()), t.Day()
	gdm := []int{0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334}
	gy2 := gy
	if gm > 2 {
		gy2 = gy + 1
	}
	days := 355666 + 365*gy + (gy2+3)/4 - (gy2+99)/100 + (gy2+399)/400 + gd + gdm[gm-1]
	jy = -1595 + 33*(days/12053)
	days %= 12053
	jy += 4 * (days / 1461)
	days %= 1461
	if days > 365 {
		jy += (days - 1) / 365
		days = (days - 1) % 365
	}
	if days < 186 {
		jm, jd = 1+days/31, 1+days%31
	} else {
		jm, jd = 7+(days-186)/30, 1+(days-186)%30
	}
	return
}

// faDigits writes ASCII digits as Persian ones and the decimal point as the
// Persian decimal sign.
func faDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune('۰' + (r - '0'))
		case r == '.':
			b.WriteRune('٫')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// faDate is "۲۸ آذر ۱۴۰۵".
func faDate(t time.Time) string {
	y, m, d := jalali(t)
	return faDigits(fmt.Sprint(d)) + " " + faMonths[m-1] + " " + faDigits(fmt.Sprint(y))
}

// faBytes is "۲۶٫۴ گیگ": one decimal below 100, none above.
func faBytes(b int64) string {
	units := []string{"بایت", "کیلوبایت", "مگابایت", "گیگ", "ترابایت"}
	v := float64(b)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	s := fmt.Sprintf("%.1f", v)
	if v >= 100 || i == 0 {
		s = fmt.Sprintf("%.0f", v)
	}
	s = strings.TrimSuffix(s, ".0")
	return faDigits(s) + " " + units[i]
}

// dayBar is one day of the last week.
type dayBar struct {
	Label  string // weekday initial
	Height int    // percent of the busiest day
	Text   string // amount, for the title
}

// pageUsage is the last week of use and what it predicts.
type pageUsage struct {
	Week     []dayBar
	Avg      string // per day, empty when nothing was used
	RunsOut  string // "about 9 days", empty when it does not apply
	EndsSoon bool   // the data runs out before the time does
}

func (s *Server) pageUsage(u *store.User, now time.Time) pageUsage {
	var out pageUsage
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -6)
	pts, _ := s.St.UsageSeries(u.ID, start.Unix())
	days := make([]int64, 7)
	for _, p := range pts {
		i := int((p.Hour - start.Unix()) / 86400) // Hour is unix seconds
		if i >= 0 && i < 7 {
			days[i] += p.Up + p.Down
		}
	}
	var max, sum int64
	for _, d := range days {
		sum += d
		if d > max {
			max = d
		}
	}
	initials := []string{"ی", "د", "س", "چ", "پ", "ج", "ش"} // Sunday first, as time.Weekday counts
	for i, d := range days {
		h := 0
		if max > 0 {
			h = int(math.Round(float64(d) * 100 / float64(max)))
		}
		if d > 0 && h < 4 {
			h = 4
		}
		out.Week = append(out.Week, dayBar{initials[start.AddDate(0, 0, i).Weekday()], h, faBytes(d)})
	}
	if sum == 0 {
		return out
	}
	avg := sum / 7
	out.Avg = faBytes(avg)
	if u.DataLimit > 0 && avg > 0 {
		left := u.DataLimit - u.Used()
		if left <= 0 {
			return out
		}
		d := int(math.Ceil(float64(left) / float64(avg)))
		out.RunsOut = faDigits(fmt.Sprint(d)) + " روز"
		if u.ExpireAt > 0 && now.Unix()+int64(d)*86400 < u.ExpireAt {
			out.EndsSoon = true
		}
	}
	return out
}

// pageDevice is one place this user is connected from, for their own page.
type pageDevice struct {
	City    string // "Tehran"
	CC      string // for the country name and flag, drawn by the page
	Org     string
	ASN     uint32
	Servers string // "Frankfurt · Tehran relay"
	Addr    string // shortened: 5.120.×.×
	Ago     string // "۲ دقیقه پیش"
}

func faAgo(sec int64) string {
	switch {
	case sec < 60:
		return "همین حالا"
	case sec < 3600:
		return faDigits(fmt.Sprint(sec/60)) + " دقیقه پیش"
	default:
		return faDigits(fmt.Sprint(sec/3600)) + " ساعت پیش"
	}
}

func (s *Server) pageDevices(u *store.User, now time.Time) []pageDevice {
	out := []pageDevice{}
	for _, d := range s.deviceViews(u) {
		p := pageDevice{Addr: maskIP(d.IP), Ago: faAgo(now.Unix() - d.LastSeen), Servers: strings.Join(d.Servers, " · ")}
		if d.Place != nil {
			p.City, p.CC = d.Place.City, d.Place.CC
		}
		if d.Net != nil {
			p.Org, p.ASN = d.Net.Org, d.Net.ASN
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].City < out[j].City })
	return out
}

// pageFacts adds the Persian figures and the extra sections to the page data.
func (s *Server) pageFacts(data map[string]any, u *store.User, now time.Time) {
	data["UsedFa"] = faBytes(u.Used())
	data["LimitFa"] = "نامحدود"
	data["LeftFa"] = "نامحدود"
	data["LeftPct"] = 100
	if u.DataLimit > 0 {
		data["LimitFa"] = faBytes(u.DataLimit)
		left := u.DataLimit - u.Used()
		if left < 0 {
			left = 0
		}
		data["LeftFa"] = faBytes(left)
		data["LeftPct"] = int(math.Round(float64(left) * 100 / float64(u.DataLimit)))
	}
	data["ExpireFa"] = ""
	if u.ExpireAt > 0 {
		data["ExpireFa"] = faDate(time.Unix(u.ExpireAt, 0))
	}
	if d, ok := data["Days"].(string); ok {
		data["DaysFa"] = faDigits(d)
	}
	data["Usage"] = s.pageUsage(u, now)
	data["Devices"] = s.pageDevices(u, now)
	if u.IPLimit > 0 {
		data["DevLimitFa"] = faDigits(fmt.Sprint(u.IPLimit))
	}
	data["DevCountFa"] = faDigits(fmt.Sprint(len(s.Mgr.Devices(u.Name))))
}
