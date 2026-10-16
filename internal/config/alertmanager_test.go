package config_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/config"
)

func selfSignedPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestAReadableAlertmanagerCAFileWithoutACertificateStopsTheServer(t *testing.T) {
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, []byte("not a certificate, but readable"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := baseEnv()
	env["REMEDY_ALERTMANAGER_CA_FILE"] = ca
	_, err := config.ServerFromEnv(func(k string) string { return env[k] })
	if err == nil || err.Error() != "REMEDY_ALERTMANAGER_CA_FILE: the file holds no certificate" {
		t.Fatalf("err = %v", err)
	}
}

func TestTheAlertmanagerCAFileIsRead(t *testing.T) {
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, selfSignedPEM(t), 0o600); err != nil {
		t.Fatal(err)
	}
	env := baseEnv()
	env["REMEDY_ALERTMANAGER_CA_FILE"] = ca
	c, err := config.ServerFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if c.AlertmanagerCAFile != ca {
		t.Errorf("AlertmanagerCAFile = %q", c.AlertmanagerCAFile)
	}
}

func TestWithoutTheVariableThereIsNoAlertmanagerCAFile(t *testing.T) {
	env := baseEnv()
	c, err := config.ServerFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if c.AlertmanagerCAFile != "" {
		t.Errorf("AlertmanagerCAFile = %q", c.AlertmanagerCAFile)
	}
}

func TestAMissingAlertmanagerCAFileStopsTheServer(t *testing.T) {
	env := baseEnv()
	env["REMEDY_ALERTMANAGER_CA_FILE"] = filepath.Join(t.TempDir(), "missing.pem")
	_, err := config.ServerFromEnv(func(k string) string { return env[k] })
	if err == nil || !strings.HasPrefix(err.Error(), "REMEDY_ALERTMANAGER_CA_FILE:") {
		t.Fatalf("err = %v", err)
	}
}
