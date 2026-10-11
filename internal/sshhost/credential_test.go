package sshhost_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/Jaydee94/remedy/internal/sshhost"
	"github.com/Jaydee94/remedy/internal/sshhost/sshtest"
)

func TestNewKeyCredential(t *testing.T) {
	plain, pub := sshtest.KeyPEM(t, "")
	wantLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))

	for name, pem := range map[string]string{
		"as written":           plain,
		"windows line endings": strings.ReplaceAll(plain, "\n", "\r\n"),
		"no final newline":     strings.TrimRight(plain, "\n"),
		"trailing spaces":      strings.ReplaceAll(plain, "\n", "  \n"),
		"padded with blanks":   "\n\n" + plain + "\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			cred, info, err := sshhost.NewKeyCredential(pem, "")
			if err != nil {
				t.Fatalf("NewKeyCredential: %v", err)
			}
			if cred.Kind != sshhost.KindKey || info.PublicLine != wantLine || !strings.HasPrefix(info.Fingerprint, "SHA256:") {
				t.Fatalf("credential = %+v, info = %+v", cred, info)
			}
		})
	}

	protected, _ := sshtest.KeyPEM(t, "correct horse")
	if _, _, err := sshhost.NewKeyCredential(protected, ""); !errors.Is(err, sshhost.ErrPassphraseNeeded) {
		t.Fatalf("no passphrase: %v", err)
	}
	if _, _, err := sshhost.NewKeyCredential(protected, "wrong"); !errors.Is(err, sshhost.ErrPassphraseWrong) {
		t.Fatalf("wrong passphrase: %v", err)
	}
	if _, info, err := sshhost.NewKeyCredential(protected, "correct horse"); err != nil || info.PublicLine == "" {
		t.Fatalf("right passphrase: %v", err)
	}
	// A passphrase for a key that has none is ignored, not an error.
	if _, _, err := sshhost.NewKeyCredential(plain, "unused"); err != nil {
		t.Fatalf("passphrase for an unprotected key: %v", err)
	}

	for name, pem := range map[string]string{
		"garbage":    "this is not a key",
		"empty":      "",
		"public key": string(ssh.MarshalAuthorizedKey(pub)),
		"too large":  strings.Repeat("A", 20<<10),
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			if _, _, err := sshhost.NewKeyCredential(pem, ""); !errors.Is(err, sshhost.ErrKeyFormat) {
				t.Fatalf("err = %v, want ErrKeyFormat", err)
			}
		})
	}
}

func TestPasswordCredential(t *testing.T) {
	if _, err := sshhost.NewPasswordCredential(""); !errors.Is(err, sshhost.ErrPasswordInvalid) {
		t.Fatalf("empty password: %v", err)
	}
	if _, err := sshhost.NewPasswordCredential(strings.Repeat("x", 257)); !errors.Is(err, sshhost.ErrPasswordInvalid) {
		t.Fatalf("long password: %v", err)
	}
	if _, err := sshhost.NewPasswordCredential("a\x00b"); !errors.Is(err, sshhost.ErrPasswordInvalid) {
		t.Fatalf("password with a NUL: %v", err)
	}
	if c, err := sshhost.NewPasswordCredential("hunter2"); err != nil || c.Kind != sshhost.KindPassword {
		t.Fatalf("password: %+v, %v", c, err)
	}
}

func TestCredentialRoundTripAndVersion(t *testing.T) {
	pem, _ := sshtest.KeyPEM(t, "pp")
	in, _, err := sshhost.NewKeyCredential(pem, "pp")
	if err != nil {
		t.Fatal(err)
	}
	blob, err := in.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	out, err := sshhost.ParseCredential(blob)
	if err != nil || out.Kind != sshhost.KindKey || out.Key.Reveal() == "" || out.Passphrase.Reveal() != "pp" {
		t.Fatalf("round trip = %+v, %v", out, err)
	}
	pw, _ := sshhost.NewPasswordCredential("hunter2")
	blob, _ = pw.Marshal()
	if got, err := sshhost.ParseCredential(blob); err != nil || got.Password.Reveal() != "hunter2" {
		t.Fatalf("password round trip = %+v, %v", got, err)
	}

	for name, bad := range map[string]string{
		"another version": `{"v":2,"kind":"password","password":"x"}`,
		"no version":      `{"kind":"password","password":"x"}`,
		"unknown kind":    `{"v":1,"kind":"token"}`,
		"not json":        `nope`,
	} {
		if _, err := sshhost.ParseCredential([]byte(bad)); !errors.Is(err, sshhost.ErrBadCredential) {
			t.Fatalf("%s: err = %v, want ErrBadCredential", name, err)
		}
	}
}

func TestACredentialNeverPrintsItsSecrets(t *testing.T) {
	pem, _ := sshtest.KeyPEM(t, "pp-DISTINCTIVE")
	c, _, _ := sshhost.NewKeyCredential(pem, "pp-DISTINCTIVE")
	for _, s := range []string{fmt.Sprint(c), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", c)} {
		if strings.Contains(s, "pp-DISTINCTIVE") || strings.Contains(s, "OPENSSH PRIVATE KEY") {
			t.Fatalf("a credential printed a secret: %s", s)
		}
	}
}
