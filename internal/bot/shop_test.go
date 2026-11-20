package bot

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/billing"
	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/util"
)

func press(chat int64, data string) map[string]any {
	return map[string]any{"callback_query": map[string]any{"id": "c" + data, "data": data,
		"message": map[string]any{"message_id": 11, "chat": map[string]any{"id": chat}}}}
}

func keyboardHas(m map[string]any, data string) bool {
	rm, _ := m["reply_markup"].(string)
	if rm == "" {
		b, _ := json.Marshal(m["reply_markup"])
		rm = string(b)
	}
	return strings.Contains(rm, `"`+data+`"`)
}

func TestBotShop(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var img bytes.Buffer
	png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 4, 4)))
	fake := &fakeTelegram{}
	// Telegram, with files: getFile names one, /file/… serves it
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/file/bot123:test/photos/"):
			w.Write(img.Bytes())
		case strings.HasSuffix(r.URL.Path, "/getFile"):
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"file_id": "big", "file_path": "photos/r.png", "file_size": img.Len()}})
		default:
			fake.ServeHTTP(w, r)
		}
	}))
	defer tg.Close()
	st.SetSetting("tg_token", "123:test")
	st.SetSetting("tg_api", tg.URL)
	st.SetSetting("tg_admins", "42")
	st.SetSetting("tg_lang", "en")

	newUser := func(name string) *store.User {
		return &store.User{Name: name, UUID: util.UUID(), Password: util.RandomString(16), SubToken: util.RandomString(20), Enabled: true}
	}
	bill := &billing.Service{St: st, NewUser: newUser, Apply: func() {}, ReceiptDir: filepath.Join(dir, "receipts")}
	if err := billing.SaveSettings(st, billing.Settings{Bot: true, Card: true, Cards: []billing.Card{{Number: "6219861012345678", Holder: "Pedram"}}, OrderHours: 24}); err != nil {
		t.Fatal(err)
	}
	p := &store.Plan{Name: "Monthly 50GB", Price: 150000, DataLimit: 50 << 30, Days: 30, StartOnFirst: true, Enabled: true}
	st.SavePlan(p)
	pid := strconv.FormatInt(p.ID, 10)

	mgr := engine.NewManager(st, dir, filepath.Join(dir, "run"), func() protocol.Env { return protocol.Env{} })
	b := New(Deps{St: st, Mgr: mgr, Sys: sysinfo.NewSampler(), Version: "test", NewUser: newUser, Bill: bill,
		SubURL:  func(u *store.User) string { return "https://example.com/sub/" + u.SubToken },
		ShopURL: func() string { return "https://example.com/sub/shop/" }})
	b.Start()

	// a stranger is offered the shop
	fake.push(msg(555, "/start"))
	fake.waitFor(t, "shop greeting", func(m map[string]any) bool { return chatOf(m) == "555" && keyboardHas(m, "sh") })
	fake.push(press(555, "sh"))
	fake.waitFor(t, "plans", func(m map[string]any) bool { return chatOf(m) == "555" && keyboardHas(m, "sp:"+pid) })
	fake.push(press(555, "sp:"+pid))
	fake.waitFor(t, "methods", func(m map[string]any) bool { return keyboardHas(m, "so:"+pid+":card") })
	fake.push(press(555, "so:"+pid+":card"))
	fake.waitFor(t, "card details", func(m map[string]any) bool {
		return chatOf(m) == "555" && strings.Contains(text(m), "6219861012345678") && strings.Contains(text(m), "receipt")
	})
	orders, _ := st.Orders(store.OrderFilter{})
	if len(orders) != 1 || orders[0].TGChat != 555 || orders[0].Status != store.OrderPending {
		t.Fatalf("order: %+v", orders)
	}
	o := orders[0]

	// the receipt photo: the admin gets it with the buttons
	fake.push(map[string]any{"message": map[string]any{"message_id": 6, "caption": "ref 77",
		"photo": []map[string]any{{"file_id": "small"}, {"file_id": "big"}},
		"chat":  map[string]any{"id": 555, "type": "private"}}})
	fake.waitFor(t, "receipt to the admin", func(m map[string]any) bool {
		return chatOf(m) == "42" && m["_method"] == "sendPhoto" && strings.Contains(text(m), o.Code) && keyboardHas(m, "oa:"+strconv.FormatInt(o.ID, 10))
	})
	fake.waitFor(t, "receipt thanks", func(m map[string]any) bool { return chatOf(m) == "555" && strings.Contains(text(m), "Got the receipt") })
	if o, _ = st.OrderByID(o.ID); o.Status != store.OrderReview || o.Receipt == "" || o.ReceiptNote != "ref 77" {
		t.Fatalf("after the photo: %+v", o)
	}

	// only admins approve
	fake.push(press(555, "oa:"+strconv.FormatInt(o.ID, 10)))
	time.Sleep(300 * time.Millisecond)
	if o, _ = st.OrderByID(o.ID); o.Status != store.OrderReview {
		t.Fatal("a buyer approved their own order")
	}
	fake.push(press(42, "oa:"+strconv.FormatInt(o.ID, 10)))
	fake.waitFor(t, "subscription to the buyer", func(m map[string]any) bool {
		return chatOf(m) == "555" && m["_method"] == "sendPhoto" && strings.Contains(text(m), "example.com/sub/")
	})
	fake.waitFor(t, "approval note", func(m map[string]any) bool { return chatOf(m) == "42" && strings.Contains(text(m), "approved") })
	u, err := st.UserByTG(555)
	if err != nil || u.DataLimit != 50<<30 {
		t.Fatalf("the buyer's subscription: %v %+v", err, u)
	}

	// renewing: a rejected tracking number, then a good one
	fake.push(press(555, "sh:r"))
	fake.waitFor(t, "renew plans", func(m map[string]any) bool { return keyboardHas(m, "sp:"+pid+":r") })
	fake.push(press(555, "so:"+pid+":card:r"))
	fake.waitFor(t, "renew card details", func(m map[string]any) bool {
		return chatOf(m) == "555" && strings.Contains(text(m), "6219861012345678") && strings.Contains(text(m), "Monthly")
	})
	fake.push(msg(555, "tracking 999"))
	fake.waitFor(t, "renew receipt to the admin", func(m map[string]any) bool { return chatOf(m) == "42" && strings.Contains(text(m), "tracking 999") })
	list, _ := st.Orders(store.OrderFilter{Status: store.OrderReview})
	if len(list) != 1 || list[0].Kind != store.OrderRenew || list[0].UserID != u.ID {
		t.Fatalf("renew order: %+v", list)
	}
	r := list[0]
	fake.push(press(42, "or:"+strconv.FormatInt(r.ID, 10)))
	fake.waitFor(t, "rejection to the buyer", func(m map[string]any) bool { return chatOf(m) == "555" && strings.Contains(text(m), "not accepted") })
	fake.push(msg(555, "tracking 1000"))
	fake.waitFor(t, "second receipt", func(m map[string]any) bool { return chatOf(m) == "42" && strings.Contains(text(m), "tracking 1000") })
	if r, _ = st.OrderByID(r.ID); r.Status != store.OrderReview {
		t.Fatalf("after a second receipt: %s", r.Status)
	}
	fake.push(press(42, "ol"))
	fake.waitFor(t, "orders waiting", func(m map[string]any) bool {
		return chatOf(m) == "42" && keyboardHas(m, "oa:"+strconv.FormatInt(r.ID, 10))
	})
}
