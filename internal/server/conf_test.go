package server

import "testing"

func TestConfName(t *testing.T) {
	for _, c := range []struct {
		title string
		id    int64
		want  string
	}{
		{"Dunvex", 12, "dunvex-12.conf"},
		{"سرور من", 5, "dunvex-5.conf"},                      // no Latin letters: the default name
		{"My Fast VPN Service", 123, "myfastvpnse-123.conf"}, // 15 before .conf, the most an interface name allows
		{"A-B_C", 7, "abc-7.conf"},
	} {
		got := confName(c.title, c.id)
		if got != c.want || len(got)-len(".conf") > 15 {
			t.Errorf("confName(%q, %d) = %q, want %q", c.title, c.id, got, c.want)
		}
	}
}
