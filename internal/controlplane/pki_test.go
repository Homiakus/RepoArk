package controlplane

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/Homiakus/repoark/internal/config"
)

func TestPKIIssuesMutuallySeparatedServerAndAgentCerts(t *testing.T) {
	d := t.TempDir()
	c := config.AgentConfig{ServerURL: "https://127.0.0.1:9790", CAPath: filepath.Join(d, "ca.pem"), CAKeyPath: filepath.Join(d, "ca-key.pem"), ServerCertPath: filepath.Join(d, "server.pem"), ServerKeyPath: filepath.Join(d, "server-key.pem"), ClientCertPath: filepath.Join(d, "agent.pem"), ClientKeyPath: filepath.Join(d, "agent-key.pem")}
	if err := InitPKI(c); err != nil {
		t.Fatal(err)
	}
	if err := IssueAgent(c, "worker-1", "", ""); err != nil {
		t.Fatal(err)
	}
	server := readCert(t, c.ServerCertPath)
	agent := readCert(t, c.ClientCertPath)
	if !hasUsage(server, x509.ExtKeyUsageServerAuth) || hasUsage(server, x509.ExtKeyUsageClientAuth) {
		t.Fatalf("bad server usages: %#v", server.ExtKeyUsage)
	}
	if !hasUsage(agent, x509.ExtKeyUsageClientAuth) || hasUsage(agent, x509.ExtKeyUsageServerAuth) {
		t.Fatalf("bad agent usages: %#v", agent.ExtKeyUsage)
	}
	if agent.Subject.CommonName != "worker-1" {
		t.Fatalf("agent identity=%q", agent.Subject.CommonName)
	}
	st, err := os.Stat(c.CAKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("CA private key permissions too broad: %o", st.Mode().Perm())
	}
}
func readCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := pem.Decode(b)
	if p == nil {
		t.Fatal("bad PEM")
	}
	c, err := x509.ParseCertificate(p.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func hasUsage(c *x509.Certificate, u x509.ExtKeyUsage) bool {
	for _, x := range c.ExtKeyUsage {
		if x == u {
			return true
		}
	}
	return false
}
