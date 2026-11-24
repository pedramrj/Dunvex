package engine

// devSeed fills the device tracker with sample connections. It is nil in every
// normal build; devseed.go sets it only when built with -tags devseed.
var devSeed func(m *Manager)
