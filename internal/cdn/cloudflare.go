package cdn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CloudflareAPI is the address of Cloudflare's API.
const CloudflareAPI = "https://api.cloudflare.com/client/v4"

// Cloudflare calls Cloudflare's API with the owner's API token. The token is
// used for one setup and never stored.
type Cloudflare struct {
	Base  string // "" = the real API (tests point it elsewhere)
	Token string
	HTTP  *http.Client
}

// APIError is an error Cloudflare reported.
type APIError struct {
	Status  int
	Code    int
	Message string
}

func (e *APIError) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("Cloudflare: %s (error %d)", e.Message, e.Code)
	}
	return "Cloudflare: " + e.Message
}

func (c *Cloudflare) call(ctx context.Context, method, path string, body, out any) error {
	base := c.Base
	if base == "" {
		base = CloudflareAPI
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(base, "/")+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // the error without the request line
		}
		return fmt.Errorf("could not reach Cloudflare's API: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var r struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return &APIError{Status: resp.StatusCode, Message: fmt.Sprintf("unexpected answer (HTTP %d)", resp.StatusCode)}
	}
	if !r.Success {
		e := &APIError{Status: resp.StatusCode, Message: fmt.Sprintf("the request failed (HTTP %d)", resp.StatusCode)}
		if len(r.Errors) > 0 {
			e.Code, e.Message = r.Errors[0].Code, r.Errors[0].Message
		}
		return e
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

// Zone is a domain in the Cloudflare account.
type Zone struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"` // active, or pending until the name servers point to Cloudflare
}

// FindZone finds the zone a name belongs to.
func (c *Cloudflare) FindZone(ctx context.Context, name string) (*Zone, error) {
	labels := strings.Split(strings.ToLower(name), ".")
	for i := 0; i+2 <= len(labels); i++ {
		var zs []Zone
		if err := c.call(ctx, "GET", "/zones?per_page=5&name="+url.QueryEscape(strings.Join(labels[i:], ".")), nil, &zs); err != nil {
			return nil, err
		}
		if len(zs) > 0 {
			return &zs[0], nil
		}
	}
	return nil, fmt.Errorf("%s is not in this Cloudflare account, or the token cannot see it: add the domain to Cloudflare and give the token access to its zone", name)
}

// Record is a DNS record.
type Record struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	TTL     int    `json:"ttl,omitempty"`
	Comment string `json:"comment,omitempty"`
}

// Records lists the records of one name.
func (c *Cloudflare) Records(ctx context.Context, zone, name string) ([]Record, error) {
	var rs []Record
	err := c.call(ctx, "GET", "/zones/"+zone+"/dns_records?per_page=100&name="+url.QueryEscape(name), nil, &rs)
	return rs, err
}

// Setting reads a zone setting's value.
func (c *Cloudflare) Setting(ctx context.Context, zone, id string) (string, error) {
	var v struct {
		Value any `json:"value"`
	}
	if err := c.call(ctx, "GET", "/zones/"+zone+"/settings/"+id, nil, &v); err != nil {
		return "", err
	}
	s, _ := v.Value.(string)
	return s, nil
}

// SetSetting changes a zone setting.
func (c *Cloudflare) SetSetting(ctx context.Context, zone, id, value string) error {
	return c.call(ctx, "PATCH", "/zones/"+zone+"/settings/"+id, map[string]string{"value": value}, nil)
}

// SetupOptions describe the setup of the proxied name.
type SetupOptions struct {
	Domain     string // the name users connect to
	IP         string // this server's IPv4 address
	Replace    bool   // records of the name that point elsewhere are replaced
	SSL        bool   // fix the SSL/TLS mode when it stops the CDN protocols
	SelfSigned bool   // this server's certificate is self-signed
	WebSockets bool   // turn WebSockets on when they are off
}

// Step is one part of the setup. ID: zone, record, ssl or websockets.
type Step struct {
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Changed bool   `json:"changed"`
	Detail  string `json:"detail,omitempty"`
	Warn    string `json:"warn,omitempty"` // a code the panel explains (pending, flexible443)
	Error   string `json:"error,omitempty"`
}

// SetupResult is what the setup did.
type SetupResult struct {
	Zone      string   `json:"zone"`
	Steps     []Step   `json:"steps"`
	Conflicts []Record `json:"conflicts,omitempty"` // records that point elsewhere: nothing was changed
	Done      bool     `json:"done"`                // the name is proxied to this server
}

// Setup points the name at this server through Cloudflare's proxy and fixes
// the zone settings the CDN protocols need. Records that point elsewhere are
// only replaced with o.Replace; without it they are reported and nothing changes.
func (c *Cloudflare) Setup(ctx context.Context, o SetupOptions, port443 bool) (*SetupResult, error) {
	res := &SetupResult{}
	z, err := c.FindZone(ctx, o.Domain)
	if err != nil {
		return nil, err
	}
	res.Zone = z.Name
	zs := Step{ID: "zone", OK: true, Detail: z.Name}
	if z.Status != "" && z.Status != "active" {
		zs.Warn = "pending"
	}
	res.Steps = append(res.Steps, zs)

	rs, err := c.Records(ctx, z.ID, o.Domain)
	if err != nil {
		return nil, err
	}
	var mine *Record
	var others []Record
	for i := range rs {
		switch r := rs[i]; {
		case r.Type == "A" && r.Content == o.IP && mine == nil:
			mine = &rs[i]
		case r.Type == "A" || r.Type == "AAAA" || r.Type == "CNAME":
			others = append(others, r)
		}
	}
	if len(others) > 0 && !o.Replace {
		res.Conflicts = others
		return res, nil
	}
	rec := Step{ID: "record", Detail: "A " + o.Domain + " → " + o.IP}
	err = func() error {
		// an A record that points elsewhere is repointed; the rest are removed
		// (an A record cannot share its name with a CNAME, and a stray A or
		// AAAA would send some users past this server)
		if mine == nil {
			for i, r := range others {
				if r.Type == "A" {
					mine = &others[i]
					others = append(others[:i:i], others[i+1:]...)
					break
				}
			}
			if mine != nil {
				rec.Changed = true
				if err := c.call(ctx, "PATCH", "/zones/"+z.ID+"/dns_records/"+mine.ID, map[string]any{"content": o.IP, "proxied": true}, nil); err != nil {
					return err
				}
				mine.Content, mine.Proxied = o.IP, true
			}
		}
		for _, r := range others {
			rec.Changed = true
			if err := c.call(ctx, "DELETE", "/zones/"+z.ID+"/dns_records/"+r.ID, nil, nil); err != nil {
				return err
			}
		}
		switch {
		case mine == nil:
			rec.Changed = true
			return c.call(ctx, "POST", "/zones/"+z.ID+"/dns_records", Record{Type: "A", Name: o.Domain, Content: o.IP, Proxied: true, TTL: 1, Comment: "Dunvex CDN"}, nil)
		case !mine.Proxied:
			rec.Changed = true
			return c.call(ctx, "PATCH", "/zones/"+z.ID+"/dns_records/"+mine.ID, map[string]any{"proxied": true}, nil)
		}
		return nil
	}()
	if err != nil {
		rec.Error = err.Error()
		res.Steps = append(res.Steps, rec)
		return res, nil
	}
	rec.OK, res.Done = true, true
	res.Steps = append(res.Steps, rec)

	// SSL/TLS: Off serves no HTTPS at all, and Full (strict) refuses a
	// self-signed certificate; both become Full, which every origin
	// certificate passes. Flexible is left alone: it speaks plain HTTP to the
	// origin only on port 443 (other ports use Full), and changing it could
	// break other sites of the zone.
	if v, err := c.Setting(ctx, z.ID, "ssl"); err != nil {
		res.Steps = append(res.Steps, Step{ID: "ssl", Error: err.Error()})
	} else {
		st := Step{ID: "ssl", OK: true, Detail: v}
		need := v == "off" || (o.SelfSigned && (v == "strict" || v == "origin_pull"))
		switch {
		case need && o.SSL:
			if err := c.SetSetting(ctx, z.ID, "ssl", "full"); err != nil {
				st.OK, st.Error = false, err.Error()
			} else {
				st.Changed, st.Detail = true, v+" → full"
			}
		case need:
			st.OK, st.Warn = false, "sslmode"
		case v == "flexible" && port443:
			st.Warn = "flexible443"
		}
		res.Steps = append(res.Steps, st)
	}
	if o.WebSockets {
		if v, err := c.Setting(ctx, z.ID, "websockets"); err != nil {
			res.Steps = append(res.Steps, Step{ID: "websockets", Error: err.Error()})
		} else if v == "off" {
			st := Step{ID: "websockets", OK: true, Changed: true, Detail: "off → on"}
			if err := c.SetSetting(ctx, z.ID, "websockets", "on"); err != nil {
				st = Step{ID: "websockets", Error: err.Error()}
			}
			res.Steps = append(res.Steps, st)
		} else {
			res.Steps = append(res.Steps, Step{ID: "websockets", OK: true, Detail: v})
		}
	}
	return res, nil
}
