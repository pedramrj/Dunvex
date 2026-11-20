package engine

import (
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

func TestMitaMetricsAndLive(t *testing.T) {
	js := `{"connections": {"CurrEstablished": 1}, "users": {"sara": {"DownloadBytes": 900, "UploadBytes": 120}, "reza": {"DownloadBytes": 0, "UploadBytes": 0}}, "traffic": {"DownloadBytes": 900}}`
	ans := protowire.AppendTag(nil, 1, protowire.BytesType)
	ans = protowire.AppendString(ans, js)
	got, err := mitaUserBytes(ans)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["sara"].Up != 120 || got["sara"].Down != 900 {
		t.Fatalf("counters: %v", got)
	}
	if _, err := mitaUserBytes([]byte{0xff}); err == nil {
		t.Error("garbage must fail")
	}

	a := []byte(`{"portBindings":[{"port":1,"protocol":"TCP"}],"users":[{"name":"sara","password":"x"}],"mtu":1400}`)
	b := []byte(`{"portBindings":[{"port":1,"protocol":"TCP"}],"users":[],"mtu":1400}`)
	c := []byte(`{"portBindings":[{"port":2,"protocol":"TCP"}],"users":[],"mtu":1400}`)
	if !usersOnlyChange(a, b) {
		t.Error("a change of users alone should reload")
	}
	if usersOnlyChange(b, c) || usersOnlyChange(nil, b) {
		t.Error("a new port must restart")
	}
}
