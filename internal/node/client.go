package node

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

func hostPort(host string, port int) string { return net.JoinHostPort(host, strconv.Itoa(port)) }

// Client is the panel's connection to one node.
type Client struct {
	Addr        string // host:port of the agent
	Fingerprint string // pinned agent certificate
	hc          *http.Client
}

// NewClient prepares a client that presents the panel's certificate and accepts
// only the node certificate with the pinned fingerprint.
func NewClient(addr, fingerprint string, panel tls.Certificate) *Client {
	cfg := &tls.Config{
		Certificates:       []tls.Certificate{panel},
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, // replaced by the pin below: nodes have self-signed certificates
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 || Fingerprint(raw[0]) != fingerprint {
				return errors.New("the node's certificate does not match its token")
			}
			return nil
		},
	}
	return &Client{Addr: addr, Fingerprint: fingerprint, hc: &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: cfg, ForceAttemptHTTP2: true, MaxIdleConns: 4, IdleConnTimeout: 90 * time.Second},
	}}
}

func (c *Client) call(ctx context.Context, method, path string, body any, header map[string]string, out any) error {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://"+c.Addr+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if res.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s", e.Error)
		}
		return fmt.Errorf("node answered %s", res.Status)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// Pair hands the node the one-time secret; the node then answers only us.
func (c *Client) Pair(ctx context.Context, secret string) error {
	return c.call(ctx, http.MethodPost, "/pair", nil, map[string]string{"X-Dunvex-Secret": secret}, nil)
}

// Status asks for the node's health.
func (c *Client) Status(ctx context.Context) (Status, error) {
	var s Status
	err := c.call(ctx, http.MethodGet, "/status", nil, nil, &s)
	return s, err
}

// Push sends the node everything it should serve.
func (c *Client) Push(ctx context.Context, s State) (Status, error) {
	var st Status
	err := c.call(ctx, http.MethodPut, "/state", s, nil, &st)
	return st, err
}

// Release tells the node it no longer belongs to this panel.
func (c *Client) Release(ctx context.Context) error {
	return c.call(ctx, http.MethodPost, "/release", nil, nil, nil)
}

// Stats acknowledges batch ack and fetches the next one.
func (c *Client) Stats(ctx context.Context, ack int64) (Batch, error) {
	var b Batch
	err := c.call(ctx, http.MethodPost, "/stats", map[string]int64{"ack": ack}, nil, &b)
	return b, err
}
