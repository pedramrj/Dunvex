package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/net/publicsuffix"

	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// Passkeys: an admin signs in with a WebAuthn credential (a fingerprint, a
// face, a phone or a security key) instead of a password and a code. The
// browser proves the user is present and verified, so a passkey sign-in is
// two-step on its own. Passkeys belong to a domain: the panel has to be opened
// by a name on HTTPS (or on localhost), never by an IP address. They are made
// for the registrable domain (esfvip.shop for vpn.esfvip.shop), so the same
// passkey works on every name of it.

// pkTTL is how long a started passkey ceremony may take.
const pkTTL = 5 * time.Minute

// pkUser is an admin as the WebAuthn library sees them.
type pkUser struct {
	a     *store.Admin
	creds []webauthn.Credential
}

func pkUserHandle(id int64) []byte                           { return []byte("dvx-admin-" + strconv.FormatInt(id, 10)) }
func (u *pkUser) WebAuthnID() []byte                         { return pkUserHandle(u.a.ID) }
func (u *pkUser) WebAuthnName() string                       { return u.a.Username }
func (u *pkUser) WebAuthnDisplayName() string                { return u.a.Username }
func (u *pkUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

// pkPending is a ceremony between its begin and finish calls.
type pkPending struct {
	data         webauthn.SessionData
	admin        int64 // 0 for a sign-in
	rpID, origin string
	expires      time.Time
}

var pkCeremonies = struct {
	sync.Mutex
	m map[string]*pkPending
}{m: map[string]*pkPending{}}

// pkStart keeps a ceremony; "" when too many are open (someone is flooding).
func pkStart(p *pkPending) string {
	pkCeremonies.Lock()
	defer pkCeremonies.Unlock()
	now := time.Now()
	for k, v := range pkCeremonies.m {
		if now.After(v.expires) {
			delete(pkCeremonies.m, k)
		}
	}
	if len(pkCeremonies.m) >= 1000 {
		return ""
	}
	p.expires = now.Add(pkTTL)
	tok := util.RandomString(32)
	pkCeremonies.m[tok] = p
	return tok
}

// pkTake hands a ceremony back once (a token works one time only).
func pkTake(tok string) *pkPending {
	pkCeremonies.Lock()
	defer pkCeremonies.Unlock()
	p := pkCeremonies.m[tok]
	delete(pkCeremonies.m, tok)
	if p == nil || time.Now().After(p.expires) {
		return nil
	}
	return p
}

var errPasskeyIP = errors.New("passkeys need the panel opened by a domain name, not an IP address")
var errPasskeyHTTP = errors.New("passkeys need the panel on HTTPS: give it a domain and its certificate on the Domains page, then turn HTTPS on for the panel")

// passkeyRP is the relying party of this request: its domain (the
// registrable one) and the exact origin the browser shows.
func passkeyRP(r *http.Request) (rpID, origin string, err error) {
	h, port, e := net.SplitHostPort(r.Host)
	if e != nil {
		h, port = r.Host, ""
	}
	h = strings.ToLower(strings.TrimSuffix(strings.Trim(h, "[]"), "."))
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	switch {
	case h == "" || net.ParseIP(h) != nil:
		return "", "", errPasskeyIP
	case h != "localhost" && scheme != "https":
		return "", "", errPasskeyHTTP
	}
	rpID = h
	if h != "localhost" {
		if d, e := publicsuffix.EffectiveTLDPlusOne(h); e == nil {
			rpID = d
		}
	}
	origin = scheme + "://" + h
	if port != "" && !(scheme == "https" && port == "443") && !(scheme == "http" && port == "80") {
		origin += ":" + port
	}
	return rpID, origin, nil
}

func newWebAuthn(rpID, origin string) (*webauthn.WebAuthn, error) {
	return webauthn.New(&webauthn.Config{RPID: rpID, RPDisplayName: "Dunvex", RPOrigins: []string{origin}})
}

// passkeyUser loads an admin with their stored credentials.
func (s *Server) passkeyUser(a *store.Admin) (*pkUser, []*store.Passkey, error) {
	list, err := s.St.PasskeysOf(a.ID)
	if err != nil {
		return nil, nil, err
	}
	u := &pkUser{a: a}
	for _, p := range list {
		var c webauthn.Credential
		if json.Unmarshal([]byte(p.Data), &c) == nil {
			u.creds = append(u.creds, c)
		}
	}
	return u, list, nil
}

func (s *Server) listPasskeys(w http.ResponseWriter, r *http.Request) {
	a := adminOf(r)
	_, list, err := s.passkeyUser(a)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	out := map[string]any{"passkeys": list, "supported": true, "max": store.MaxPasskeys}
	if rpID, _, err := passkeyRP(r); err != nil {
		out["supported"], out["reason"] = false, err.Error()
	} else {
		out["rpId"] = rpID
	}
	writeJSON(w, 200, out)
}

func (s *Server) beginPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	a := adminOf(r)
	rpID, origin, err := passkeyRP(r)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	var in struct{ Password string }
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	// a stolen session alone cannot add a way in
	if bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(in.Password)) != nil {
		time.Sleep(400 * time.Millisecond)
		fail(w, 400, "current password is wrong")
		return
	}
	u, list, err := s.passkeyUser(a)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if len(list) >= store.MaxPasskeys {
		fail(w, 400, "you have as many passkeys as an admin may keep; remove one first")
		return
	}
	wa, err := newWebAuthn(rpID, origin)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	var exclude []protocol.CredentialDescriptor
	for _, c := range u.creds {
		exclude = append(exclude, c.Descriptor())
	}
	creation, sess, err := wa.BeginRegistration(u,
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey: protocol.ResidentKeyRequirementRequired, RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification: protocol.VerificationRequired}),
		webauthn.WithExclusions(exclude),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation))
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	tok := pkStart(&pkPending{data: *sess, admin: a.ID, rpID: rpID, origin: origin})
	if tok == "" {
		fail(w, http.StatusTooManyRequests, "too many passkey requests at once; try again in a few minutes")
		return
	}
	writeJSON(w, 200, map[string]any{"token": tok, "options": creation})
}

func (s *Server) finishPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	a := adminOf(r)
	var in struct {
		Token      string          `json:"token"`
		Name       string          `json:"name"`
		Credential json.RawMessage `json:"credential"`
	}
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	p := pkTake(in.Token)
	rpID, origin, err := passkeyRP(r)
	if p == nil || p.admin != a.ID || err != nil || p.rpID != rpID || p.origin != origin {
		fail(w, 400, "this passkey request ended or is not yours; start again")
		return
	}
	parsed, err := protocol.ParseCredentialCreationResponseBody(bytes.NewReader(in.Credential))
	if err != nil {
		fail(w, 400, "the browser's answer could not be read: "+err.Error())
		return
	}
	u, _, err := s.passkeyUser(a)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	wa, err := newWebAuthn(rpID, origin)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	cred, err := wa.CreateCredential(u, p.data, parsed)
	if err != nil {
		fail(w, 400, "the passkey was not accepted: "+webauthnReason(err))
		return
	}
	data, _ := json.Marshal(cred)
	name := strings.TrimSpace(strings.Join(strings.Fields(in.Name), " "))
	if utf8.RuneCountInString(name) > 40 {
		name = string([]rune(name)[:40])
	}
	if name == "" {
		name = "Passkey"
	}
	pk := &store.Passkey{AdminID: a.ID, CredID: base64.RawURLEncoding.EncodeToString(cred.ID), Data: string(data),
		Name: name, RPID: rpID, CreatedAt: time.Now().Unix()}
	if err := s.St.CreatePasskey(pk); err != nil {
		fail(w, 400, "this passkey is already saved")
		return
	}
	s.St.Log("auth", "admin %s added the passkey %q", a.Username, name)
	writeJSON(w, 200, pk)
}

func (s *Server) deletePasskey(w http.ResponseWriter, r *http.Request) {
	a := adminOf(r)
	ok, err := s.St.DeletePasskey(pathID(r), a.ID)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if !ok {
		fail(w, 404, "no such passkey")
		return
	}
	s.St.Log("auth", "admin %s removed a passkey", a.Username)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// beginPasskeyLogin starts a sign-in with any passkey of this panel: the
// browser offers the ones it has for the domain, so no username is typed.
func (s *Server) beginPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if s.tooManyFailures(ip) {
		fail(w, http.StatusTooManyRequests, "too many attempts, try again in a few minutes")
		return
	}
	if s.SetupPending() {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "finish the first-run setup first", "needSetup": true})
		return
	}
	rpID, origin, err := passkeyRP(r)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	wa, err := newWebAuthn(rpID, origin)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	assertion, sess, err := wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	tok := pkStart(&pkPending{data: *sess, rpID: rpID, origin: origin})
	if tok == "" {
		fail(w, http.StatusTooManyRequests, "too many passkey requests at once; try again in a few minutes")
		return
	}
	writeJSON(w, 200, map[string]any{"token": tok, "options": assertion})
}

func (s *Server) finishPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if s.tooManyFailures(ip) {
		fail(w, http.StatusTooManyRequests, "too many attempts, try again in a few minutes")
		return
	}
	var in struct {
		Token      string          `json:"token"`
		Credential json.RawMessage `json:"credential"`
	}
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	p := pkTake(in.Token)
	rpID, origin, err := passkeyRP(r)
	if p == nil || p.admin != 0 || err != nil || p.rpID != rpID || p.origin != origin {
		fail(w, 400, "this sign-in ended; try again")
		return
	}
	refuse := func(why string) {
		s.recordFailure(ip)
		s.logFailure(ip, "", why)
		time.Sleep(600 * time.Millisecond)
		fail(w, http.StatusUnauthorized, "this passkey cannot sign in here")
	}
	parsed, err := protocol.ParseCredentialRequestResponseBody(bytes.NewReader(in.Credential))
	if err != nil {
		refuse("unreadable passkey answer")
		return
	}
	var used *store.Passkey
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		pk, err := s.St.PasskeyByCredID(base64.RawURLEncoding.EncodeToString(rawID))
		if err != nil {
			return nil, errors.New("unknown passkey")
		}
		if !bytes.Equal(userHandle, pkUserHandle(pk.AdminID)) {
			return nil, errors.New("passkey of another account")
		}
		a, err := s.St.AdminByID(pk.AdminID)
		if err != nil {
			return nil, errors.New("its admin is gone")
		}
		u, _, err := s.passkeyUser(a)
		if err != nil {
			return nil, err
		}
		used = pk
		return u, nil
	}
	wa, err := newWebAuthn(rpID, origin)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	user, cred, err := wa.ValidatePasskeyLogin(handler, p.data, parsed)
	if err != nil || used == nil {
		refuse("passkey not accepted: " + webauthnReason(err))
		return
	}
	a := user.(*pkUser).a
	if !a.Enabled {
		refuse("passkey of a disabled admin " + a.Username)
		return
	}
	if cred.Authenticator.CloneWarning {
		// the signature counter went back: the key may have been copied
		refuse("passkey counter went back (possible copy) for " + a.Username)
		return
	}
	data, _ := json.Marshal(cred)
	s.St.UsePasskey(used.ID, string(data), time.Now().Unix())
	s.issueSession(w, r, a)
	s.St.Log("auth", "admin %s signed in with the passkey %q from %s", a.Username, used.Name, ip)
	writeJSON(w, 200, a)
}

// webauthnReason is the library's error in a line (its details when it has them).
func webauthnReason(err error) string {
	if err == nil {
		return "unknown"
	}
	var pe *protocol.Error
	if errors.As(err, &pe) && pe.DevInfo != "" {
		return pe.Details + " (" + pe.DevInfo + ")"
	}
	return err.Error()
}
