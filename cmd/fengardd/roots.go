package main

import (
	"crypto/x509"

	"golang.org/x/crypto/x509roots/fallback/bundle"
)

// routers often have no ca bundle so tls downloads need our own copy, but it's 1mb parsed so
// only when the router really has none. go never looks at the fallback if system roots exist
func fallbackRoots() bool {
	if p, err := x509.SystemCertPool(); err == nil && !p.Equal(x509.NewCertPool()) {
		return false
	}
	p := x509.NewCertPool()
	for c := range bundle.Roots() {
		cert, err := x509.ParseCertificate(c.Certificate)
		if err != nil {
			continue
		}
		if c.Constraint == nil {
			p.AddCert(cert)
		} else {
			p.AddCertWithConstraint(cert, c.Constraint)
		}
	}
	x509.SetFallbackRoots(p)
	return true
}
