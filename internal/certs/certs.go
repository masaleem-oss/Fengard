// unique ca per box and it only signs blocked hosts so it cant fake real sites
package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	caCertFile = "ca.crt"
	caKeyFile  = "ca.key"
	leafTTL    = 7 * 24 * time.Hour
	maxCached  = 2048 // cap so random hostnames cant eat memory
)

type caState struct {
	cert    *x509.Certificate
	certPEM []byte
	keyPEM  []byte
	key     *ecdsa.PrivateKey
}

type Authority struct {
	dir     string
	st      atomic.Pointer[caState]
	leafKey *ecdsa.PrivateKey // one key for all leafs cheaper on routers

	Allowed func(host string) bool

	// ip sans so https to the router ip works
	DashboardNames []string
	DashboardIPs   []net.IP

	mu    sync.Mutex
	cache map[string]*tls.Certificate
	dash  *tls.Certificate
}

func LoadOrCreate(dir string) (*Authority, error) {
	certPath, keyPath := filepath.Join(dir, caCertFile), filepath.Join(dir, caKeyFile)
	certPEM, err := os.ReadFile(certPath)
	if errors.Is(err, os.ErrNotExist) {
		if certPEM, err = create(certPath, keyPath); err != nil {
			return nil, fmt.Errorf("create CA: %w", err)
		}
	} else if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	st, err := parseBundle(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("CA files: %w", err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	a := &Authority{dir: dir, leafKey: leafKey, cache: map[string]*tls.Certificate{}}
	a.st.Store(st)
	return a, nil
}

func create(certPath, keyPath string) ([]byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	id := make([]byte, 4)
	rand.Read(id)
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "Fengard Local CA " + strings.ToUpper(hex.EncodeToString(id)), Organization: []string{"Fengard"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return certPEM, os.WriteFile(certPath, certPEM, 0o644)
}

func parseBundle(certPEM, keyPEM []byte) (*caState, error) {
	cb, _ := pem.Decode(certPEM)
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || cb.Type != "CERTIFICATE" || kb == nil || kb.Type != "EC PRIVATE KEY" {
		return nil, errors.New("expected a CERTIFICATE and an EC PRIVATE KEY")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, err
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, err
	}
	if !cert.IsCA || cert.KeyUsage&x509.KeyUsageCertSign == 0 {
		return nil, errors.New("certificate is not a certificate authority")
	}
	if pub, ok := cert.PublicKey.(*ecdsa.PublicKey); !ok || !pub.Equal(&key.PublicKey) {
		return nil, errors.New("private key does not match the certificate")
	}
	if time.Until(cert.NotAfter) < 30*24*time.Hour {
		return nil, errors.New("certificate expires within 30 days")
	}
	return &caState{
		cert: cert, key: key,
		certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cb.Bytes}),
		keyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb.Bytes}),
	}, nil
}

// has the private key so treat it as a secret
func (a *Authority) Export() []byte {
	s := a.st.Load()
	return append(append([]byte{}, s.certPEM...), s.keyPEM...)
}

// drops old leafs so new connections use the new ca
func (a *Authority) Import(bundle []byte) error {
	var certPEM, keyPEM []byte
	for rest := bundle; ; {
		var b *pem.Block
		if b, rest = pem.Decode(rest); b == nil {
			break
		}
		switch b.Type {
		case "CERTIFICATE":
			certPEM = pem.EncodeToMemory(b)
		case "EC PRIVATE KEY":
			keyPEM = pem.EncodeToMemory(b)
		}
	}
	if certPEM == nil || keyPEM == nil {
		return errors.New("not a Fengard certificate bundle (needs the certificate and its private key)")
	}
	st, err := parseBundle(certPEM, keyPEM)
	if err != nil {
		return err
	}
	if a.dir != "" {
		keyPath, certPath := filepath.Join(a.dir, caKeyFile), filepath.Join(a.dir, caCertFile)
		if err := os.WriteFile(keyPath+".tmp", st.keyPEM, 0o600); err != nil {
			return err
		}
		if err := os.WriteFile(certPath+".tmp", st.certPEM, 0o644); err != nil {
			return err
		}
		if err := os.Rename(keyPath+".tmp", keyPath); err != nil {
			return err
		}
		if err := os.Rename(certPath+".tmp", certPath); err != nil {
			return err
		}
	}
	a.st.Store(st)
	a.mu.Lock()
	a.cache = map[string]*tls.Certificate{}
	a.dash = nil
	a.mu.Unlock()
	return nil
}

func (a *Authority) CertPEM() []byte { return a.st.Load().certPEM }

// android wants der
func (a *Authority) CertDER() []byte { return a.st.Load().cert.Raw }

func (a *Authority) Fingerprint() string {
	sum := sha256.Sum256(a.st.Load().cert.Raw)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

func (a *Authority) Expires() time.Time { return a.st.Load().cert.NotAfter }

// profiles cant have an icon so the names are what shows in settings
func (a *Authority) MobileConfig(network string) []byte {
	if network == "" {
		network = "Fengard"
	}
	title := "Fengard · " + network
	id := strings.ToLower(strings.ReplaceAll(a.Name(), " ", "-"))
	uuid := func(seed string) string {
		h := sha256.Sum256([]byte(seed + a.Fingerprint()))
		return fmt.Sprintf("%X-%X-%X-%X-%X", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
	}
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array>
		<dict>
			<key>PayloadCertificateFileName</key><string>fengard-ca.cer</string>
			<key>PayloadContent</key><data>%s</data>
			<key>PayloadDisplayName</key><string>%s</string>
			<key>PayloadDescription</key><string>Trusts this network's Fengard gateway so blocked sites show its block page.</string>
			<key>PayloadIdentifier</key><string>net.fengard.%s.cert</string>
			<key>PayloadType</key><string>com.apple.security.root</string>
			<key>PayloadUUID</key><string>%s</string>
			<key>PayloadVersion</key><integer>1</integer>
		</dict>
	</array>
	<key>PayloadDescription</key><string>Lets %s show its block page on HTTPS sites. The certificate is unique to this gateway and only signs sites the network blocks.</string>
	<key>PayloadDisplayName</key><string>%s</string>
	<key>PayloadIdentifier</key><string>net.fengard.%s</string>
	<key>PayloadOrganization</key><string>Fengard</string>
	<key>PayloadType</key><string>Configuration</string>
	<key>PayloadUUID</key><string>%s</string>
	<key>PayloadVersion</key><integer>1</integer>
</dict>
</plist>
`, base64.StdEncoding.EncodeToString(a.st.Load().cert.Raw), a.Name(), id, uuid("cert"), network, title, id, uuid("profile")))
}

func (a *Authority) Name() string { return a.st.Load().cert.Subject.CommonName }

// no sni means someone hit the ip so give the dashboard cert
func (a *Authority) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	host := strings.ToLower(strings.TrimSuffix(hello.ServerName, "."))
	if host == "" || a.isDashboard(host) {
		return a.dashboardCert()
	}
	if a.Allowed == nil || !a.Allowed(host) {
		return nil, fmt.Errorf("refusing certificate for non-blocked host %q", host)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if c, ok := a.cache[host]; ok && time.Until(c.Leaf.NotAfter) > time.Hour {
		return c, nil
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(leafTTL),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	c, err := a.issue(tmpl)
	if err != nil {
		return nil, err
	}
	if len(a.cache) >= maxCached {
		for k := range a.cache { // random eviction
			delete(a.cache, k)
			break
		}
	}
	a.cache[host] = c
	return c, nil
}

// caller holds a.mu
func (a *Authority) issue(tmpl *x509.Certificate) (*tls.Certificate, error) {
	s := a.st.Load()
	der, err := x509.CreateCertificate(rand.Reader, tmpl, s.cert, &a.leafKey.PublicKey, s.key)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: [][]byte{der, s.cert.Raw}, PrivateKey: a.leafKey, Leaf: leaf}, nil
}

func (a *Authority) isDashboard(host string) bool {
	for _, n := range a.DashboardNames {
		if host == n {
			return true
		}
	}
	if ip := net.ParseIP(host); ip != nil {
		for _, d := range a.DashboardIPs {
			if d.Equal(ip) {
				return true
			}
		}
	}
	return false
}

func (a *Authority) dashboardCert() (*tls.Certificate, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.dash != nil && time.Until(a.dash.Leaf.NotAfter) > 24*time.Hour {
		return a.dash, nil
	}
	names := append([]string{}, a.DashboardNames...)
	if len(names) == 0 {
		names = []string{"fengard.lan"}
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: names[0], Organization: []string{"Fengard"}},
		DNSNames:     names,
		IPAddresses:  a.DashboardIPs,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	c, err := a.issue(tmpl)
	if err != nil {
		return nil, err
	}
	a.dash = c
	return c, nil
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}
