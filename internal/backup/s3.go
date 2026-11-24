package backup

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Bucket is an S3-compatible bucket (AWS, Cloudflare R2, Backblaze B2,
// ArvanCloud, MinIO…), reached path-style with Signature Version 4.
type Bucket struct {
	Endpoint string // https://s3.example.com
	Region   string // "us-east-1" when empty; R2 takes "auto"
	Name     string
	Prefix   string // a folder in the bucket
	Access   string
	Secret   string
	Client   *http.Client
	now      func() time.Time
}

// Bucket is the configured bucket, nil when the owner set none.
func (s *Service) Bucket() *Bucket {
	b := &Bucket{
		Endpoint: strings.TrimRight(strings.TrimSpace(s.setting(KeyS3Endpoint, "")), "/"),
		Region:   strings.TrimSpace(s.setting(KeyS3Region, "")),
		Name:     strings.TrimSpace(s.setting(KeyS3Bucket, "")),
		Prefix:   strings.Trim(strings.TrimSpace(s.setting(KeyS3Prefix, "")), "/"),
		Access:   strings.TrimSpace(s.setting(KeyS3Access, "")),
		Secret:   strings.TrimSpace(s.setting(KeyS3Secret, "")),
	}
	if b.Endpoint == "" || b.Name == "" || b.Access == "" || b.Secret == "" {
		return nil
	}
	return b
}

// CheckBucket says what is wrong with bucket settings, if anything visible.
func CheckBucket(endpoint, bucket string) error {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return errors.New("the endpoint must be an address like https://s3.example.com")
	}
	if bucket == "" || strings.ContainsAny(bucket, "/ ") {
		return errors.New("write the bucket's name (no slashes)")
	}
	return nil
}

func (b *Bucket) region() string {
	if b.Region == "" {
		return "us-east-1"
	}
	return b.Region
}

// awsEscape encodes a path the way Signature V4 wants: everything but
// unreserved characters and "/".
func awsEscape(p string) string {
	var sb strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c == '/' || c == '-' || c == '_' || c == '.' || c == '~' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			sb.WriteByte(c)
		} else {
			fmt.Fprintf(&sb, "%%%02X", c)
		}
	}
	return sb.String()
}

// Put stores data under the bucket's prefix as name.
func (b *Bucket) Put(name string, data []byte) error {
	key := name
	if b.Prefix != "" {
		key = b.Prefix + "/" + name
	}
	u, err := url.Parse(b.Endpoint)
	if err != nil || u.Host == "" {
		return fmt.Errorf("bad endpoint %q", b.Endpoint)
	}
	path := strings.TrimRight(u.Path, "/") + "/" + b.Name + "/" + key
	req, err := http.NewRequest(http.MethodPut, u.Scheme+"://"+u.Host+awsEscape(path), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(data))
	sum := sha256.Sum256(data)
	b.sign(req, hex.EncodeToString(sum[:]))
	c := b.Client
	if c == nil {
		c = &http.Client{Timeout: 5 * time.Minute}
	}
	res, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("the bucket is not reachable: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode/100 == 2 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	var e struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	if xml.Unmarshal(body, &e) == nil && e.Code != "" {
		return fmt.Errorf("the bucket said %s: %s", e.Code, e.Message)
	}
	return fmt.Errorf("the bucket answered %s", res.Status)
}

func hmacSHA(key []byte, s string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(s))
	return m.Sum(nil)
}

// sign adds Signature Version 4 to req (every header it has is signed).
func (b *Bucket) sign(req *http.Request, payloadHash string) {
	now := time.Now
	if b.now != nil {
		now = b.now
	}
	t := now().UTC()
	amzDate := t.Format("20060102T150405Z")
	day := amzDate[:8]
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	headers := map[string]string{"host": req.URL.Host}
	for k, v := range req.Header {
		headers[strings.ToLower(k)] = strings.Join(strings.Fields(strings.Join(v, ",")), " ")
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, k := range names {
		canonHeaders.WriteString(k + ":" + headers[k] + "\n")
	}
	signed := strings.Join(names, ";")

	q := req.URL.Query()
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var query []string
	for _, k := range keys {
		for _, v := range q[k] {
			query = append(query, url.QueryEscape(k)+"="+url.QueryEscape(v))
		}
	}
	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	canonical := strings.Join([]string{req.Method, path, strings.Join(query, "&"), canonHeaders.String(), signed, payloadHash}, "\n")
	scope := day + "/" + b.region() + "/s3/aws4_request"
	sum := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(sum[:])
	key := hmacSHA(hmacSHA(hmacSHA(hmacSHA([]byte("AWS4"+b.Secret), day), b.region()), "s3"), "aws4_request")
	req.Header.Set("Authorization", fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		b.Access, scope, signed, hex.EncodeToString(hmacSHA(key, toSign))))
}
