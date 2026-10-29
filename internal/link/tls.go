package link

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// loadOrCreateCert returns the persisted self-signed certificate, creating it
// on first use. The phone pins the SHA-256 fingerprint delivered in the pairing
// QR code, so the SANs only need to be plausible, not exact.
func loadOrCreateCert(dir string) (tls.Certificate, string, error) {
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")

	if cert, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		return cert, fingerprint(cert.Certificate[0]), nil
	}

	certPEM, keyPEM, err := newSelfSigned("wintp")
	if err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return tls.Certificate{}, "", err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("load generated cert: %w", err)
	}
	return cert, fingerprint(cert.Certificate[0]), nil
}

// newSelfSigned creates a 10-year ECDSA P-256 self-signed server certificate
// and returns it and its key as PEM.
func newSelfSigned(commonName string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"wintp"},
		IPAddresses:           append([]net.IP{net.IPv4(127, 0, 0, 1)}, lanIPs()...),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	// PKCS#8, because the Android side parses this key with Java, which only reads PKCS#8.
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// lanIPs returns the machine's private IPv4 addresses.
func lanIPs() []net.IP {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []net.IP
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipn.IP.To4()
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || !ip.IsPrivate() {
			continue
		}
		out = append(out, ip)
	}
	return out
}
