package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSupportStore(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ali := &User{Name: "ali", UUID: "u1", Password: "p", SubToken: "tok-ali-0123456789", Enabled: true, TGChat: 77, OwnerID: 5}
	st.CreateUser(ali)
	now := time.Now().Unix()

	// the first message makes the thread; it is unread and waits for an answer
	th, m, err := st.SupportIncoming(SupportIn{Chat: 77, UserID: ali.ID, OwnerID: 5, Name: "Ali", Username: "ali_tg", Text: "  it does not\nconnect  ", TGMsg: 11, At: now - 30})
	if err != nil || th.Unread != 1 || !th.Waiting() || th.LastText != "it does not connect" || m.ID == 0 || th.Messages != 1 {
		t.Fatalf("first: %+v %+v %v", th, m, err)
	}
	th, _, _ = st.SupportIncoming(SupportIn{Chat: 77, UserID: ali.ID, OwnerID: 5, Text: "", Photo: "a.jpg", TGMsg: 12, At: now - 20})
	if th.Unread != 2 || th.LastText != "📷" || th.Name != "Ali" {
		t.Fatalf("second: %+v", th)
	}
	if n := st.SupportSentSince(77, now-25); n != 1 {
		t.Fatalf("sent since: %d", n)
	}

	// seen: the receipts go once
	if ids := st.SupportMarkSeen(th.ID); len(ids) != 2 {
		t.Fatalf("seen ids: %v", ids)
	}
	if ids := st.SupportMarkSeen(th.ID); len(ids) != 0 {
		t.Fatalf("seen twice: %v", ids)
	}
	th, _ = st.SupportThreadByID(th.ID)
	if th.Unread != 0 || !th.Waiting() {
		t.Fatalf("after seen: %+v", th)
	}

	// an answer: no longer waiting, the person's messages answered
	ans, ids, err := st.SupportAnswer(th.ID, "boss", "try again", "", now-10)
	if err != nil || !ans.Staff || ans.By != "boss" || len(ids) != 2 {
		t.Fatalf("answer: %+v %v %v", ans, ids, err)
	}
	th, _ = st.SupportThreadByID(th.ID)
	if th.Waiting() || !th.LastStaff || th.Messages != 3 {
		t.Fatalf("after answer: %+v", th)
	}
	msgs, more, _ := st.SupportMessages(th.ID, 0, 2)
	if len(msgs) != 2 || !more || msgs[1].ID != ans.ID || !msgs[0].Answered {
		t.Fatalf("page: %+v %v", msgs, more)
	}
	if older, more, _ := st.SupportMessages(th.ID, msgs[0].ID, 10); len(older) != 1 || more {
		t.Fatalf("older: %d %v", len(older), more)
	}

	// a guest (no subscription) and the filters
	guest, _, _ := st.SupportIncoming(SupportIn{Chat: 99, Name: "Stranger", Username: "who", Text: "price?", At: now})
	if guest.ID != th.ID+1 {
		t.Fatalf("thread ids skip: %d after %d", guest.ID, th.ID)
	}
	if l, _ := st.SupportThreads(SupportFilter{}); len(l) != 2 || l[0].ID != guest.ID {
		t.Fatalf("all, newest first: %d", len(l))
	}
	if l, _ := st.SupportThreads(SupportFilter{Owner: 5}); len(l) != 1 || l[0].Chat != 77 {
		t.Fatalf("reseller 5 sees %d", len(l))
	}
	if l, _ := st.SupportThreads(SupportFilter{Owner: 6}); len(l) != 0 {
		t.Fatalf("reseller 6 sees %d", len(l))
	}
	for q, want := range map[string]int{"ali": 1, "@ali_tg": 1, "stranger": 1, "99": 1, "nobody": 0, "%": 0} {
		if l, _ := st.SupportThreads(SupportFilter{Q: q}); len(l) != want {
			t.Fatalf("search %q: %d", q, len(l))
		}
	}
	if l, _ := st.SupportThreads(SupportFilter{Status: "waiting"}); len(l) != 1 || l[0].ID != guest.ID {
		t.Fatalf("waiting: %d", len(l))
	}
	if c := st.SupportCount(0); c.Open != 2 || c.Waiting != 1 || c.Unread != 1 || c.Closed != 0 {
		t.Fatalf("counts: %+v", c)
	}

	// the user moves to another reseller: the thread follows; once deleted, the stored owner holds
	ali.OwnerID = 6
	st.UpdateUser(ali)
	if st.SupportOwnerOf(th) != 6 {
		t.Fatal("owner did not follow the user")
	}
	st.DeleteUser(ali.ID)
	if st.SupportOwnerOf(th) != 5 {
		t.Fatal("owner of a deleted user's thread")
	}

	// close, and a new message opens it again
	st.SetSupportOpen(th.ID, false, "boss", now)
	if c := st.SupportCount(0); c.Closed != 1 {
		t.Fatalf("closed: %+v", c)
	}
	th, _, _ = st.SupportIncoming(SupportIn{Chat: 77, Text: "still broken", At: now + 1})
	if !th.Open || th.ClosedBy != "" || th.UserID != 0 {
		t.Fatalf("reopened: %+v", th)
	}
	if id, err := st.SupportPhotoThread("a.jpg"); err != nil || id != th.ID {
		t.Fatalf("photo thread: %d %v", id, err)
	}

	// retention: the newest 2 per thread; then everything older than now
	photos, removed, err := st.PruneSupport(2, 0)
	if err != nil || removed != 2 || len(photos) != 1 || photos[0] != "a.jpg" {
		t.Fatalf("keep 2: %v %d %v", photos, removed, err)
	}
	th, _ = st.SupportThreadByID(th.ID)
	if th.Messages != 2 || th.Unread != 1 {
		t.Fatalf("after keep: %+v", th)
	}
	if _, removed, _ := st.PruneSupport(0, now); removed != 1 {
		t.Fatalf("by age: %d", removed)
	}
	if _, err := st.SupportThreadByID(guest.ID); err != nil {
		t.Fatal("the guest's thread went with nothing old in it")
	}
	if _, removed, _ := st.PruneSupport(0, now+10); removed != 2 {
		t.Fatalf("all old: %d", removed)
	}
	if l, _ := st.SupportThreads(SupportFilter{}); len(l) != 0 {
		t.Fatalf("empty threads stay: %d", len(l))
	}

	// delete gives the photos back
	th, _, _ = st.SupportIncoming(SupportIn{Chat: 5, Photo: "b.png", At: now})
	if photos, err := st.DeleteSupportThread(th.ID); err != nil || len(photos) != 1 || photos[0] != "b.png" {
		t.Fatalf("delete: %v %v", photos, err)
	}
	if _, err := st.SupportThreadByChat(5); err != ErrNotFound {
		t.Fatal("thread still there")
	}
}
