package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestTags(t *testing.T) {
	got := NormalizeTags([]string{"  VIP ", "vip", "family,friends", "", "  ", strings.Repeat("x", 40), "مشتری ویژه", "دوست،فامیل"})
	want := []string{"VIP", "family friends", strings.Repeat("x", MaxTagLen), "مشتری ویژه", "دوست فامیل"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
	many := []string{}
	for i := 0; i < 20; i++ {
		many = append(many, string(rune('a'+i)))
	}
	if len(NormalizeTags(many)) != MaxTagsPer {
		t.Fatal("no limit")
	}

	u := &User{Tags: []string{"VIP"}}
	if !u.HasTag("vip") || u.AddTag("Vip") || !u.AddTag("family") || !u.RemoveTag("VIP") || u.RemoveTag("nope") || len(u.Tags) != 1 {
		t.Fatalf("tag ops: %q", u.Tags)
	}

	// they are stored, read back and counted
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &User{Name: "a", UUID: "1", Password: "p", SubToken: "ta-0123456789", Enabled: true, Tags: []string{"vip", " VIP", "iran"}}
	b := &User{Name: "b", UUID: "2", Password: "p", SubToken: "tb-0123456789", Enabled: true}
	st.CreateUser(a)
	st.CreateUser(b)
	if x, _ := st.UserByName("a"); strings.Join(x.Tags, ",") != "vip,iran" {
		t.Fatalf("stored: %q", x.Tags)
	}
	if x, _ := st.UserByName("b"); x.Tags == nil || len(x.Tags) != 0 {
		t.Fatalf("no tags should read as an empty list: %#v", x.Tags)
	}
	b.Tags = []string{"VIP"}
	st.UpdateUser(b)
	users, _ := st.Users()
	c := CountTags(users)
	if len(c) != 2 || c[0] != (TagCount{"vip", 2}) || c[1] != (TagCount{"iran", 1}) {
		t.Fatalf("counts: %+v", c)
	}
}
