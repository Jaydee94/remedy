package sshhost

import (
	"encoding/json"
	"errors"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/Jaydee94/remedy/internal/secret"
)

const (
	KindKey      = "key"
	KindPassword = "password"

	maxPasswordLen    = 256
	maxKeyLen         = 16 << 10
	credentialVersion = 1
)

var (
	ErrKeyFormat        = errors.New("that is not a private key I can read")
	ErrPassphraseNeeded = errors.New("the key is protected by a passphrase")
	ErrPassphraseWrong  = errors.New("the passphrase does not open the key")
	ErrPasswordInvalid  = errors.New("the password must have 1 to 256 characters")
	ErrBadCredential    = errors.New("the stored login cannot be read")
)

// Credential is a login. Its secrets are secret.Value: printing or logging one gives "***".
type Credential struct {
	Kind       string
	Key        secret.Value // the private key, as text
	Passphrase secret.Value
	Password   secret.Value
}

// KeyInfo is what can be shown about a private key without showing it.
type KeyInfo struct {
	PublicLine  string // the authorized_keys line
	Fingerprint string // SHA256:...
}

var okKeyTypes = map[string]bool{
	ssh.KeyAlgoED25519: true, ssh.KeyAlgoRSA: true,
	ssh.KeyAlgoECDSA256: true, ssh.KeyAlgoECDSA384: true, ssh.KeyAlgoECDSA521: true,
}

// normalizePEM makes a pasted key parseable: any line ending, trailing blanks, no final newline, blank lines around it.
func normalizePEM(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n")) + "\n"
}

func parseSigner(pemBytes []byte, passphrase string) (ssh.Signer, error) {
	raw, err := ssh.ParseRawPrivateKey(pemBytes)
	var missing *ssh.PassphraseMissingError
	switch {
	case errors.As(err, &missing):
		if passphrase == "" {
			return nil, ErrPassphraseNeeded
		}
		if raw, err = ssh.ParseRawPrivateKeyWithPassphrase(pemBytes, []byte(passphrase)); err != nil {
			return nil, ErrPassphraseWrong
		}
	case err != nil:
		return nil, ErrKeyFormat
	}
	signer, err := ssh.NewSignerFromKey(raw)
	if err != nil || !okKeyTypes[signer.PublicKey().Type()] {
		return nil, ErrKeyFormat
	}
	return signer, nil
}

// NewKeyCredential checks a pasted private key (and its passphrase, if it needs one) and derives what may be shown of it.
// Nothing in an error contains the key.
func NewKeyCredential(pemText, passphrase string) (Credential, KeyInfo, error) {
	if len(pemText) > maxKeyLen {
		return Credential{}, KeyInfo{}, ErrKeyFormat
	}
	pemText = normalizePEM(pemText)
	signer, err := parseSigner([]byte(pemText), passphrase)
	if err != nil {
		return Credential{}, KeyInfo{}, err
	}
	pub := signer.PublicKey()
	return Credential{Kind: KindKey, Key: secret.NewValue(pemText), Passphrase: secret.NewValue(passphrase)},
		KeyInfo{PublicLine: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))), Fingerprint: ssh.FingerprintSHA256(pub)}, nil
}

func NewPasswordCredential(pw string) (Credential, error) {
	if pw == "" || len(pw) > maxPasswordLen || strings.ContainsRune(pw, 0) {
		return Credential{}, ErrPasswordInvalid
	}
	return Credential{Kind: KindPassword, Password: secret.NewValue(pw)}, nil
}

// wire is the sealed form. v lets a later layout be told from this one.
type wire struct {
	V          int    `json:"v"`
	Kind       string `json:"kind"`
	Key        string `json:"key,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
	Password   string `json:"password,omitempty"`
}

// Marshal is what gets sealed. Handle the result as a secret.
func (c Credential) Marshal() ([]byte, error) {
	return json.Marshal(wire{V: credentialVersion, Kind: c.Kind, Key: c.Key.Reveal(), Passphrase: c.Passphrase.Reveal(), Password: c.Password.Reveal()})
}

func ParseCredential(b []byte) (Credential, error) {
	var w wire
	if err := json.Unmarshal(b, &w); err != nil || w.V != credentialVersion {
		return Credential{}, ErrBadCredential
	}
	switch w.Kind {
	case KindKey:
		return Credential{Kind: KindKey, Key: secret.NewValue(w.Key), Passphrase: secret.NewValue(w.Passphrase)}, nil
	case KindPassword:
		return Credential{Kind: KindPassword, Password: secret.NewValue(w.Password)}, nil
	}
	return Credential{}, ErrBadCredential
}

// methods are the ways the client offers this login: a key signs; a password is sent as a password and, for servers that only
// ask for it that way, as the answer to every keyboard-interactive question.
func (c Credential) methods() ([]ssh.AuthMethod, error) {
	switch c.Kind {
	case KindKey:
		signer, err := parseSigner([]byte(c.Key.Reveal()), c.Passphrase.Reveal())
		if err != nil {
			return nil, ErrBadCredential
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	case KindPassword:
		pw := c.Password.Reveal()
		return []ssh.AuthMethod{
			ssh.Password(pw),
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = pw
				}
				return answers, nil
			}),
		}, nil
	}
	return nil, ErrBadCredential
}
