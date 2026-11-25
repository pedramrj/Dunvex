package server

import "github.com/pedramrj/dunvex/internal/store"

// seedNodes fills the node hub with a sample Bridge relay for screenshots and
// demos. It is nil in every normal build; devseed.go sets it only when built
// with -tags devseed (and run with DUNVEX_DEV_SEED=1). Never in a release.
var seedNodes func(s *Server)

// skipSync keeps the sync loop from touching a seeded node (which has no real
// agent). The default never skips; devseed.go overrides it.
var skipSync = func(*store.Node) bool { return false }
