package server

import (
	"log"
	"strings"
	"time"

	"github.com/pedramrj/dunvex/internal/tlsmgr"
)

// RunCertRenewal renews this server's Let's Encrypt certificate a month
// before it expires (checked twice a day), and restarts the cores so they
// serve the new one. The panel reads its certificate files again by itself.
func (s *Server) RunCertRenewal(stop <-chan struct{}) {
	t := time.NewTicker(12 * time.Hour)
	defer t.Stop()
	last := ""
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		if msg := s.renewCert(); msg != last {
			if msg != "" {
				s.St.Log("tls", "the certificate could not be renewed: %s", msg)
			}
			last = msg
		}
	}
}

// renewCert renews the certificate when it is due and explains a failure.
func (s *Server) renewCert() string {
	domain := strings.TrimSpace(s.St.GetSetting("domain", ""))
	if domain == "" || s.St.GetSetting("cert_mode", "self") != "acme" {
		return ""
	}
	cert, key, renewed, err := tlsmgr.Renew(s.CertDir(), domain, s.St.GetSetting("acme_email", ""))
	if err != nil {
		log.Printf("certificate renewal for %s: %v", domain, err)
		return tlsmgr.Explain(err)
	}
	if renewed {
		s.St.SetSetting("cert_file", cert)
		s.St.SetSetting("key_file", key)
		s.St.Log("tls", "certificate renewed for %s", domain)
		s.Mgr.CertsRenewed()
	}
	return ""
}
