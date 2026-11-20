package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pedramrj/dunvex/internal/cdn"
	"github.com/pedramrj/dunvex/internal/node"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
)

// ---------------------------------------------------------------- domains

// cleanNodeDomain checks a node's domain ("" = none). A relay runs no
// protocols and has no use for one.
func cleanNodeDomain(d string, relay bool) (string, string) {
	d = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
	switch {
	case d == "":
		return "", ""
	case relay:
		return "", "a relay runs no protocols, so it needs no domain"
	case !cdn.IsHostname(d):
		return "", fmt.Sprintf("%q is not a domain name", d)
	}
	return d, ""
}

// moveNodeDomain points the TLS protocols of a node that used its old domain
// (as their server name) at the new one.
func (s *Server) moveNodeDomain(n *store.Node, old string) {
	if n.Domain == "" {
		return
	}
	for _, in := range s.inboundsOn(n.ID) {
		p := protocol.Get(in.Preset)
		if p == nil || p.Security != "tls" {
			continue
		}
		if sni := in.Str("sni"); sni == "" || strings.EqualFold(sni, old) {
			in.Params["sni"] = n.Domain
			s.St.UpdateInbound(in)
		}
	}
	s.KickNodes()
}

// ---------------------------------------------------------------- updates

// nodeUpdate says how a node can be brought to the panel's version.
type nodeUpdate struct {
	Panel string `json:"panel"` // the panel's version
	// panel: the panel sends its own binary (same system); github: the node
	// downloads the panel's release; "": it cannot (see Reason)
	Via    string `json:"via"`
	Newer  bool   `json:"newer"` // the node is newer than the panel
	Reason string `json:"reason,omitempty"`
}

// updateFor is the update a node needs, or nil when it runs the panel's version.
func (s *Server) updateFor(st *node.Status) *nodeUpdate {
	if st.Version == "" || st.Version == s.Version {
		return nil
	}
	u := &nodeUpdate{Panel: s.Version}
	switch {
	case compareVersions(st.Version, s.Version) > 0:
		u.Newer = true
	case st.Arch == "":
		u.Reason = "old" // before v1.18 nodes take no updates from the panel
	case st.Arch == runtime.GOARCH && st.OS == runtime.GOOS:
		u.Via = "panel"
	case strings.HasPrefix(s.Version, "v"):
		u.Via = "github"
	default:
		u.Reason = "dev"
	}
	return u
}

// updateReasons explain why a node cannot be updated from the panel.
var updateReasons = map[string]string{
	"old": "this server runs a version that cannot be updated from the panel yet; update it once on the server itself: dunvex update, then systemctl restart dunvex",
	"dev": node.ErrNoRelease.Error(),
}

// compareVersions compares "v1.17.0"-style versions (-1, 0, 1); anything
// else (a development build) counts as older than a release.
func compareVersions(a, b string) int {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	switch {
	case !oka && !okb:
		return 0
	case !oka:
		return -1
	case !okb:
		return 1
	}
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// selfBinary is the panel's own executable and its SHA-256.
func (s *Server) selfBinary() ([]byte, string, error) {
	var b []byte
	var err error
	if s.SelfBinary != nil {
		b, err = s.SelfBinary()
	} else {
		var p string
		if p, err = os.Executable(); err == nil {
			if p, err = filepath.EvalSymlinks(p); err == nil {
				b, err = os.ReadFile(p)
			}
		}
	}
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(b)
	return b, hex.EncodeToString(sum[:]), nil
}

type nodeUpdateResult struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	From    string `json:"from"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

// updateNode brings one node to the panel's version; it restarts by itself.
func (s *Server) updateOne(ctx context.Context, n *store.Node, who string) nodeUpdateResult {
	v := s.viewNode(n, nil)
	res := nodeUpdateResult{ID: n.ID, Name: n.Name}
	if v.Status != nil {
		res.From = v.Status.Version
	}
	switch {
	case !v.Online:
		res.Error = n.Name + " is not reachable now"
		return res
	case v.Update == nil:
		res.Error = n.Name + " already runs " + s.Version
		return res
	case v.Update.Newer:
		res.Error = n.Name + " runs a newer version (" + res.From + ") than this panel; update the panel first"
		return res
	case v.Update.Via == "":
		res.Error = updateReasons[v.Update.Reason]
		return res
	}
	c, err := s.hub().client(n)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	if v.Update.Via == "panel" {
		bin, sum, err := s.selfBinary()
		if err != nil {
			res.Error = "cannot read this panel's binary: " + err.Error()
			return res
		}
		res.Version, err = c.Update(ctx, bin, sum, s.Version)
		if err != nil {
			res.Error = err.Error()
		}
	} else if res.Version, err = c.Update(ctx, nil, "", s.Version); err != nil {
		res.Error = err.Error()
	}
	if res.Error == "" {
		s.St.Log("update", "%s updated the server %s from %s to %s", who, n.Name, res.From, res.Version)
		// it restarts with the new binary; look again once it is back
		go func() {
			for _, d := range []time.Duration{5, 10, 20} {
				time.Sleep(d * time.Second)
				s.KickNodes()
			}
		}()
	} else {
		s.St.Log("update", "server %s could not be updated: %s", n.Name, res.Error)
	}
	return res
}

func (s *Server) updateNodeVersion(w http.ResponseWriter, r *http.Request) {
	n, err := s.St.NodeByID(pathID(r))
	if err != nil {
		fail(w, 404, "server not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	res := s.updateOne(ctx, n, adminOf(r).Username)
	if res.Error != "" {
		fail(w, 400, res.Error)
		return
	}
	writeJSON(w, 200, res)
}

// updateAllNodes updates every reachable node that is behind the panel, a
// few at a time.
func (s *Server) updateAllNodes(w http.ResponseWriter, r *http.Request) {
	list, err := s.St.Nodes()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	who := adminOf(r).Username
	out := []nodeUpdateResult{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	limit := make(chan struct{}, 3)
	for _, n := range list {
		v := s.viewNode(n, nil)
		if !v.Online || v.Update == nil || v.Update.Newer || v.Update.Via == "" {
			continue
		}
		wg.Add(1)
		go func(n *store.Node) {
			defer wg.Done()
			limit <- struct{}{}
			defer func() { <-limit }()
			res := s.updateOne(ctx, n, who)
			mu.Lock()
			out = append(out, res)
			mu.Unlock()
		}(n)
	}
	wg.Wait()
	writeJSON(w, 200, map[string]any{"results": out})
}
