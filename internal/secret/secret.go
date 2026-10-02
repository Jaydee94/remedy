// Package secret seals values with a master key and keeps secrets out of logs.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

const keyLen = 32

// ErrOpen means a sealed value could not be opened: wrong key, wrong context or tampered data.
var ErrOpen = errors.New("secret: cannot open sealed value (wrong key, wrong context or tampered data)")

// Key is a 256-bit master key. Its text form never contains the key.
type Key struct{ b [keyLen]byte }

// ParseKey decodes a standard Base64 string that must hold exactly 32 bytes.
func ParseKey(s string) (Key, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Key{}, errors.New("master key is empty")
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return Key{}, errors.New("master key is not valid Base64")
	}
	if len(raw) != keyLen {
		return Key{}, fmt.Errorf("master key must be %d bytes, got %d", keyLen, len(raw))
	}
	var k Key
	copy(k.b[:], raw)
	return k, nil
}

func (k Key) String() string   { return "***" }
func (k Key) GoString() string { return "secret.Key(***)" }

func (k Key) aead() (cipher.AEAD, error) {
	block, err := aes.NewCipher(k.b[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Seal encrypts plaintext with AES-256-GCM and a fresh random nonce. aad binds the result to a
// context, for example a database row, so that it cannot be moved to another one. The nonce is
// prepended to the ciphertext.
func (k Key) Seal(plaintext []byte, aad string) ([]byte, error) {
	g, err := k.aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, plaintext, []byte(aad)), nil
}

// Open reverses Seal. It returns ErrOpen for a wrong key, a wrong context and any tampering.
func (k Key) Open(sealed []byte, aad string) ([]byte, error) {
	g, err := k.aead()
	if err != nil {
		return nil, err
	}
	if len(sealed) < g.NonceSize() {
		return nil, ErrOpen
	}
	nonce, ciphertext := sealed[:g.NonceSize()], sealed[g.NonceSize():]
	plaintext, err := g.Open(nil, nonce, ciphertext, []byte(aad))
	if err != nil {
		return nil, ErrOpen
	}
	return plaintext, nil
}

// Value is a secret string. Printing, formatting, JSON encoding and logging it all yield "***";
// the content is only reachable through Reveal.
type Value struct{ s string }

func NewValue(s string) Value { return Value{s: s} }

// Reveal returns the secret. Call it only where the secret is actually used.
func (v Value) Reveal() string { return v.s }

func (Value) String() string               { return "***" }
func (Value) GoString() string             { return "secret.Value(***)" }
func (Value) MarshalJSON() ([]byte, error) { return []byte(`"***"`), nil }
func (Value) LogValue() slog.Value         { return slog.StringValue("***") }
