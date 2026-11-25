//go:build !linux

package bridge

// Off Linux the raw-socket fake-TCP carrier is unavailable; the panel shows it
// down with the reason. Production runs Linux on both ends.

// NewFakeTCPClient is unsupported off Linux.
func NewFakeTCPClient(_ string, _ int) (Carrier, error) {
	return unsupportedCarrier{name: "faketcp"}, nil
}

// NewFakeTCPServer is unsupported off Linux.
func NewFakeTCPServer(_ int) Carrier { return unsupportedCarrier{name: "faketcp"} }
