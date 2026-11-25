package server

// The user's page in Persian (the default) or English. The visitor picks the
// language with the switch at the top (?lang=en or ?lang=fa); the choice is
// kept in a cookie for a year so the page opens in it next time.

import (
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

type pageLang struct{ en bool }

const pageLangCookie = "dvx_page_lang"

func (s *Server) pageLangFor(w http.ResponseWriter, r *http.Request) pageLang {
	if q := r.URL.Query().Get("lang"); q == "en" || q == "fa" {
		http.SetCookie(w, &http.Cookie{Name: pageLangCookie, Value: q, Path: s.SubPath(), MaxAge: 365 * 86400,
			HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
		return pageLang{en: q == "en"}
	}
	if c, err := r.Cookie(pageLangCookie); err == nil {
		return pageLang{en: c.Value == "en"}
	}
	return pageLang{}
}

func (l pageLang) code() string {
	if l.en {
		return "en"
	}
	return "fa"
}

func (l pageLang) dir() string {
	if l.en {
		return "ltr"
	}
	return "rtl"
}

// num writes a number in the page's digits.
func (l pageLang) num(v any) string {
	s := fmt.Sprint(v)
	if l.en {
		return s
	}
	return faDigits(s)
}

// bytes is "26.4 GB" or "۲۶٫۴ گیگ": one decimal below 100, none above.
func (l pageLang) bytes(b int64) string {
	if !l.en {
		return faBytes(b)
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	v, i := float64(b), 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	s := fmt.Sprintf("%.1f", v)
	if v >= 100 || i == 0 {
		s = fmt.Sprintf("%.0f", v)
	}
	return strings.TrimSuffix(s, ".0") + " " + units[i]
}

// date is "3 Feb 2027" or the Persian calendar date.
func (l pageLang) date(t time.Time) string {
	if !l.en {
		return faDate(t)
	}
	return t.Format("2 Jan 2006")
}

func (l pageLang) ago(sec int64) string {
	if !l.en {
		return faAgo(sec)
	}
	switch {
	case sec < 60:
		return "just now"
	case sec < 3600:
		return fmt.Sprint(sec/60) + " min ago"
	default:
		return fmt.Sprint(sec/3600) + " h ago"
	}
}

// weekdays are the initials under the week's bars, Sunday first.
func (l pageLang) weekdays() []string {
	if l.en {
		return []string{"S", "M", "T", "W", "T", "F", "S"}
	}
	return []string{"ی", "د", "س", "چ", "پ", "ج", "ش"}
}

// pageText holds every sentence of the page as {Persian, English}. A sentence
// with a value inside is split into an "A" part before it and a "B" part after.
var pageText = map[string][2]string{
	"remaining":    {"باقی‌مانده", "remaining"},
	"days":         {"روز", "days"},
	"of":           {"از", "of"},
	"used":         {"مصرف شده", "used"},
	"until":        {"تا", "until"},
	"devAtOnce":    {"دستگاه هم‌زمان", "devices at once"},
	"pausedUntil":  {"متوقف تا", "Paused until"},
	"pending":      {"شروع از اولین اتصال", "Starts on first connection"},
	"online":       {"آنلاین", "Online"},
	"active":       {"فعال", "Active"},
	"unlimited":    {"نامحدود", "Unlimited"},
	"renewNow":     {"با دکمهٔ «تمدید» همین حالا تمدیدش کنید.", "Renew it now with the Renew button."},
	"renewSupport": {"برای تمدید با پشتیبانی تماس بگیرید.", "Contact support to renew."},
	"pausedA":      {"از تعداد مجاز دستگاه هم‌زمان بیشتر وصل شده بودید؛ اتصال تا ساعت", "You were connected from more devices than allowed, so the connection is paused until"},
	"pausedB":      {"متوقف است و بعد خودکار برمی‌گردد.", "and then comes back by itself."},
	"renewTitle":   {"تمدید یا خرید حجم", "Renew or buy data"},
	"renewSub":     {"پلن‌ها و پرداخت در فروشگاه", "Plans and payment in the store"},
	"renew":        {"تمدید", "Renew"},
	"connect":      {"اتصال", "Connect"},
	"step1A":       {"برنامهٔ", "Install"},
	"step1B":       {"را نصب کنید", ""},
	"storePhone":   {"از فروشگاه برنامه‌های گوشی", "From your phone's app store"},
	"storeIos":     {"رایگان، از App Store", "Free, from the App Store"},
	"storeAndroid": {"رایگان، از Google Play یا سایت برنامه", "Free, from Google Play or the app's site"},
	"storeDesk":    {"از سایت برنامه، برای ویندوز و مک", "From the app's site, for Windows and Mac"},
	"step2":        {"اشتراک را اضافه کنید", "Add your subscription"},
	"addTo":        {"افزودن به", "Add to"},
	"step3":        {"در برنامه «اتصال» را بزنید", "Tap Connect in the app"},
	"otherApp":     {"برنامهٔ دیگری دارید؟", "Using another app?"},
	"replay":       {"دوباره نشان بده", "Show it again"},
	"subLink":      {"لینک اشتراک", "Subscription link"},
	"copy":         {"کپی", "Copy"},
	"showQR":       {"نمایش QR", "Show QR"},
	"week":         {"مصرف ۷ روز گذشته", "Last 7 days"},
	"perDayA":      {"روزی", ""},
	"perDayB":      {"", "a day"},
	"runsA":        {"با همین روند، حجم شما حدود", "At this pace your data runs out in about"},
	"runsB":        {"دیگر تمام می‌شود", ""},
	"runsSoon":     {"، زودتر از پایان زمان اشتراک", ", before your time ends"},
	"devices":      {"دستگاه‌های وصل", "Connected devices"},
	"unknownPlace": {"مکان نامشخص", "Unknown place"},
	"server":       {"سرور", "server"},
	"devHint":      {"فقط دستگاه‌های خود شما در ۵ دقیقهٔ گذشته. اگر دستگاهی را نمی‌شناسید، به پشتیبانی بگویید.", "Only your own devices in the last 5 minutes. If you don't recognise one, tell support."},
	"configs":      {"کانفیگ‌ها", "Configs"},
	"download":     {"دانلود", "Download"},
	"amneziaHint":  {"در برنامهٔ AmneziaVPN یا AmneziaWG فایل را وارد کنید یا کد QR را اسکن کنید.", "Import the file in AmneziaVPN or AmneziaWG, or scan the QR code."},
	"sshHint":      {"در برنامهٔ Hiddify این لینک را وارد کنید (یا لینک اشتراک را). در NetMod، HTTP Injector و برنامه‌های دیگر SSH این مشخصات را دستی وارد کنید:", "Import this link in the Hiddify app (or the subscription link). In NetMod, HTTP Injector and other SSH apps, type these details in by hand:"},
	"slipHint":     {"در برنامهٔ SlipNet لینک را وارد کنید (Import) یا کد QR را اسکن کنید.", "Import the link in SlipNet or scan the QR code."},
	"slipByHand":   {" در DNSTT.XYZ هم می‌توانید این مشخصات را دستی وارد کنید:", " You can also type these details into DNSTT.XYZ by hand:"},
	"botTitle":     {"هشدار حجم و تاریخ در تلگرام", "Data and date alerts in Telegram"},
	"openBot":      {"باز کردن ربات", "Open the bot"},
	"support":      {"پشتیبانی", "Support"},
	"contact":      {"تماس", "Contact"},
	"limited":      {"حجم اشتراک تمام شده", "Your data has run out"},
	"expired":      {"مدت اشتراک تمام شده", "Your subscription has expired"},
	"disabled":     {"حساب غیرفعال است", "The account is disabled"},
	"otherLang":    {"EN", "فا"},
	"otherHref":    {"?lang=en", "?lang=fa"},
	"otherName":    {"English", "فارسی"},
}

// texts is the page's sentences in its language.
func (l pageLang) texts() map[string]string {
	out := make(map[string]string, len(pageText))
	i := 0
	if l.en {
		i = 1
	}
	for k, v := range pageText {
		out[k] = v[i]
	}
	return out
}

func (l pageLang) t(key string) string {
	if l.en {
		return pageText[key][1]
	}
	return pageText[key][0]
}

// mapPoint is a place on the page's small map, rounded to a tenth of a degree
// (the city, which the device list already names). No address is sent.
type mapPoint struct {
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
	Name string  `json:"name"`
}

// pageMap is the user's own connections for the map: where each device is and
// which servers it reached.
type pageMap struct {
	Land    string     `json:"land"` // the 2.5° land mask, hex
	W       int        `json:"w"`
	H       int        `json:"h"`
	Devices []mapDev   `json:"devices"`
	Servers []mapPoint `json:"servers"`
}

type mapDev struct {
	mapPoint
	Servers []int `json:"servers"` // indexes into Servers
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// pageMapFor places this user's devices and the servers they reached. It is
// nil when no device has a known place.
func (s *Server) pageMapFor(views []deviceView) *pageMap {
	var m pageMap
	idx, seen := map[int64]int{}, map[string]bool{}
	var servers []mapServer
	for _, d := range views {
		if d.Place == nil || (d.Place.Lat == 0 && d.Place.Lon == 0) {
			continue
		}
		if servers == nil {
			servers = s.mapServers()
		}
		dev := mapDev{mapPoint: mapPoint{round1(d.Place.Lat), round1(d.Place.Lon), d.Place.City}, Servers: []int{}}
		for _, id := range d.Via {
			for _, sv := range servers {
				if sv.ID != id || sv.Place == nil {
					continue
				}
				i, ok := idx[id]
				if !ok {
					i = len(m.Servers)
					idx[id] = i
					m.Servers = append(m.Servers, mapPoint{round1(sv.Place.Lat), round1(sv.Place.Lon), sv.Name})
				}
				dev.Servers = append(dev.Servers, i)
			}
		}
		key := fmt.Sprint(dev.Lat, dev.Lon, dev.Servers)
		if seen[key] {
			continue // two devices in one city on the same servers are one route
		}
		seen[key] = true
		m.Devices = append(m.Devices, dev)
	}
	if len(m.Devices) == 0 {
		return nil
	}
	if m.Servers == nil {
		m.Servers = []mapPoint{}
	}
	m.Land, m.W, m.H = landMaskHex, landMaskW, landMaskH
	return &m
}
