package controlplane

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Homiakus/repoark/internal/config"
)

func InitPKI(c config.AgentConfig) error {
	if err := os.MkdirAll(filepath.Dir(c.CAPath), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(c.CAPath); errors.Is(err, os.ErrNotExist) {
		pub, priv, _ := ed25519.GenerateKey(rand.Reader)
		now := time.Now().UTC()
		tmpl := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "RepoArk Control Plane CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(10, 0, 0), KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature, BasicConstraintsValid: true, IsCA: true}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
		if err != nil {
			return err
		}
		if err := writeCert(c.CAPath, der); err != nil {
			return err
		}
		if err := writePrivate(c.CAKeyPath, priv); err != nil {
			return err
		}
	}
	if _, err := os.Stat(c.ServerCertPath); errors.Is(err, os.ErrNotExist) {
		return issue(c, "repoark-control-plane", c.ServerCertPath, c.ServerKeyPath, true)
	}
	return nil
}

func IssueAgent(c config.AgentConfig, name, certPath, keyPath string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("agent name is empty")
	}
	if name == LocalWorkerAffinity {
		return errors.New("agent name is reserved for local control-plane workers")
	}
	if certPath == "" {
		certPath = c.ClientCertPath
	}
	if keyPath == "" {
		keyPath = c.ClientKeyPath
	}
	return issue(c, name, certPath, keyPath, false)
}

func issue(c config.AgentConfig, name, certPath, keyPath string, server bool) error {
	caCert, caKey, err := loadCA(c)
	if err != nil {
		return err
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().UTC()
	tmpl := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: name}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(2, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature}
	if server {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		tmpl.DNSNames = []string{"localhost"}
		tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
		if u, err := url.Parse(c.ServerURL); err == nil {
			host := u.Hostname()
			if ip := net.ParseIP(host); ip != nil {
				tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
			} else if host != "" {
				tmpl.DNSNames = append(tmpl.DNSNames, host)
			}
		}
	} else {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		tmpl.DNSNames = []string{name}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, pub, caKey)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return err
	}
	if err := writeCert(certPath, der); err != nil {
		return err
	}
	return writePrivate(keyPath, priv)
}

func loadCA(c config.AgentConfig) (*x509.Certificate, ed25519.PrivateKey, error) {
	b, err := os.ReadFile(c.CAPath)
	if err != nil {
		return nil, nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, nil, errors.New("invalid CA certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, err
	}
	kb, err := os.ReadFile(c.CAKeyPath)
	if err != nil {
		return nil, nil, err
	}
	kblock, _ := pem.Decode(kb)
	if kblock == nil {
		return nil, nil, errors.New("invalid CA key PEM")
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(kblock.Bytes)
	if err != nil {
		return nil, nil, err
	}
	key, ok := keyAny.(ed25519.PrivateKey)
	if !ok {
		return nil, nil, errors.New("CA key is not Ed25519")
	}
	return cert, key, nil
}
func writeCert(path string, der []byte) error {
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}
func writePrivate(path string, key ed25519.PrivateKey) error {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600)
}
func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return n
}

func PKISummary(c config.AgentConfig) string {
	return fmt.Sprintf("CA=%s server=%s client=%s", c.CAPath, c.ServerCertPath, c.ClientCertPath)
}
