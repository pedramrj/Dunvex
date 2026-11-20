package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protowire"
)

// rawCodec passes pre-encoded protobuf bytes through gRPC, so Dunvex needs no
// generated stubs for the tiny StatsService API shared by Xray and sing-box.
type rawCodec struct{}

func (rawCodec) Marshal(v any) ([]byte, error) { return *(v.(*[]byte)), nil }
func (rawCodec) Unmarshal(data []byte, v any) error {
	*(v.(*[]byte)) = append([]byte(nil), data...)
	return nil
}
func (rawCodec) Name() string { return "proto" }

type statsClient struct {
	mu    sync.Mutex
	conns map[string]*grpc.ClientConn
}

var stats = &statsClient{conns: map[string]*grpc.ClientConn{}}

func (s *statsClient) conn(addr string) (*grpc.ClientConn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.conns[addr]; ok {
		return c, nil
	}
	c, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	s.conns[addr] = c
	return c, nil
}

// QueryStats calls StatsService.QueryStats and returns name→value.
// service is "xray.app.stats.command.StatsService" or "v2ray.core.app.stats.command.StatsService".
func QueryStats(addr, service string, reset bool) (map[string]int64, error) {
	c, err := stats.conn(addr)
	if err != nil {
		return nil, err
	}
	var req []byte
	if reset {
		req = protowire.AppendTag(req, 2, protowire.VarintType)
		req = protowire.AppendVarint(req, 1)
	}
	var resp []byte
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := c.Invoke(ctx, "/"+service+"/QueryStats", &req, &resp, grpc.ForceCodec(rawCodec{})); err != nil {
		return nil, err
	}
	return decodeStats(resp)
}

func decodeStats(b []byte) (map[string]int64, error) {
	out := map[string]int64{}
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return nil, fmt.Errorf("bad stats response")
		}
		b = b[n:]
		if num == 1 && typ == protowire.BytesType {
			msg, m := protowire.ConsumeBytes(b)
			if m < 0 {
				return nil, fmt.Errorf("bad stat entry")
			}
			b = b[m:]
			name, val := decodeStat(msg)
			out[name] += val
			continue
		}
		m := protowire.ConsumeFieldValue(num, typ, b)
		if m < 0 {
			return nil, fmt.Errorf("bad stats field")
		}
		b = b[m:]
	}
	return out, nil
}

func decodeStat(b []byte) (name string, val int64) {
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return
		}
		b = b[n:]
		switch {
		case num == 1 && typ == protowire.BytesType:
			s, m := protowire.ConsumeString(b)
			if m < 0 {
				return
			}
			name, b = s, b[m:]
		case num == 2 && typ == protowire.VarintType:
			v, m := protowire.ConsumeVarint(b)
			if m < 0 {
				return
			}
			val, b = int64(v), b[m:]
		default:
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				return
			}
			b = b[m:]
		}
	}
	return
}

// Counter is a parsed stats key: kind ("user"/"inbound"), name, direction ("uplink"/"downlink").
type Counter struct {
	Kind, Name, Dir string
}

// ParseCounter parses "user>>>alice>>>traffic>>>uplink".
func ParseCounter(key string) (Counter, bool) {
	p := strings.Split(key, ">>>")
	if len(p) != 4 || p[2] != "traffic" {
		return Counter{}, false
	}
	return Counter{Kind: p[0], Name: p[1], Dir: p[3]}, true
}
