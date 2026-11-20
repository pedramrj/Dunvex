package routing

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/warp"
)

func TestCleanDomains(t *testing.T) {
	got, err := CleanDomains([]string{"https://Chat.OpenAI.com/c/123", "*.example.org, example.org", " .foo.ir\nbar.co.uk:443 ", "x.com،y.com"})
	want := []string{"chat.openai.com", "example.org", "foo.ir", "bar.co.uk", "x.com", "y.com"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%v %v", err, got)
	}
	for _, bad := range []string{"localhost", "exa mple.com", "-bad.com", "a..b"} {
		if _, err := CleanDomains([]string{bad}); err == nil && bad != "exa mple.com" {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestSettings(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := Load(st)
	if !reflect.DeepEqual(s, Defaults()) || s.Active() || s.UsesWarp() || !s.ClientIran {
		t.Fatalf("defaults: %+v", s)
	}
	for _, bad := range []Settings{
		{Iran: "maybe"},
		{Warp: "half"},
		{Warp: WarpSites},
		{Warp: WarpSites, WarpGroups: []string{"nope"}},
		{BlockDomains: []string{"not a domain!"}},
	} {
		if _, err := Save(st, bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	s, err = Save(st, Settings{Iran: IranWarp, Warp: WarpSites, WarpGroups: []string{"ai", "netflix"}, WarpDomains: []string{"https://api.x.ai/"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := Load(st); !reflect.DeepEqual(got, s) || !got.Active() || !got.UsesWarp() {
		t.Fatalf("saved %+v, read %+v", s, got)
	}
	if c := s.WarpSiteCodes(); !reflect.DeepEqual(c, []string{"category-ai-!cn", "netflix"}) || s.WarpDomains[0] != "api.x.ai" {
		t.Errorf("codes %v domains %v", c, s.WarpDomains)
	}
	if (Settings{BlockAds: true, Iran: IranDirect, Warp: WarpOff}).UsesWarp() {
		t.Error("ads alone use WARP")
	}

	if Account(st) != nil {
		t.Fatal("an account out of nowhere")
	}
	a := &warp.Account{ID: "id", Token: "t", PrivateKey: "k", PeerKey: "p", AddressV4: "172.16.0.2"}
	SaveAccount(st, a)
	if got := Account(st); got == nil || got.PeerKey != "p" {
		t.Fatalf("account: %+v", got)
	}
	SaveAccount(st, nil)
	if Account(st) != nil {
		t.Error("the account was not forgotten")
	}
}
