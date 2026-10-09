// Package sshtest is an SSH server for tests. It runs in the test process, accepts only the login it is given, and answers
// exactly the commands it is given. It lets the host key be swapped between connections.
package sshtest

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// Reply is the answer to one command line.
type Reply struct {
	Out   string
	Exit  int
	Delay time.Duration // how long the server waits before it answers
}

type Config struct {
	Password   string        // the password that logs in; empty accepts none
	Key        ssh.PublicKey // the public key that logs in; nil accepts none
	Commands   map[string]Reply
	HostSigner ssh.Signer // nil: a new ed25519 key
}

type Server struct {
	Host string
	Port int

	cfg      Config
	ln       net.Listener
	mu       sync.Mutex
	signer   ssh.Signer
	attempts int
	open     int
	maxOpen  int
	conns    map[net.Conn]struct{}
}

// Start listens on a free port of 127.0.0.1 and serves until the test ends.
func Start(t testing.TB, cfg Config) *Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, cfg: cfg, ln: ln, signer: cfg.HostSigner, conns: map[net.Conn]struct{}{}}
	if s.signer == nil {
		s.signer = NewSigner(t)
	}
	go s.accept()
	t.Cleanup(s.Close)
	return s
}

func (s *Server) Close() {
	_ = s.ln.Close()
	s.mu.Lock()
	for c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()
}

func (s *Server) HostKey() ssh.PublicKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.signer.PublicKey()
}

// SetHostSigner makes the server present another host key from the next connection on.
func (s *Server) SetHostSigner(sg ssh.Signer) { s.mu.Lock(); s.signer = sg; s.mu.Unlock() }

// Attempts counts the authentication attempts (a password or a public key offered). A connection that stops at the key
// exchange makes none.
func (s *Server) Attempts() int { s.mu.Lock(); defer s.mu.Unlock(); return s.attempts }

// MaxConcurrent is the highest number of connections that were open at the same time.
func (s *Server) MaxConcurrent() int { s.mu.Lock(); defer s.mu.Unlock(); return s.maxOpen }

func (s *Server) attempt() { s.mu.Lock(); s.attempts++; s.mu.Unlock() }

func (s *Server) accept() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(c)
	}
}

func (s *Server) serverConfig() *ssh.ServerConfig {
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			s.attempt()
			if s.cfg.Password != "" && string(pw) == s.cfg.Password {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
		PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			s.attempt()
			if s.cfg.Key != nil && bytes.Equal(k.Marshal(), s.cfg.Key.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
	}
	s.mu.Lock()
	cfg.AddHostKey(s.signer)
	s.mu.Unlock()
	return cfg
}

func (s *Server) handle(nc net.Conn) {
	s.mu.Lock()
	s.conns[nc] = struct{}{}
	s.open++
	if s.open > s.maxOpen {
		s.maxOpen = s.open
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, nc)
		s.open--
		s.mu.Unlock()
		_ = nc.Close()
	}()

	sc, chans, reqs, err := ssh.NewServerConn(nc, s.serverConfig())
	if err != nil {
		return
	}
	defer sc.Close()
	go ssh.DiscardRequests(reqs)
	for nch := range chans {
		if nch.ChannelType() != "session" {
			_ = nch.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		ch, creqs, err := nch.Accept()
		if err != nil {
			return
		}
		s.session(ch, creqs)
	}
}

// session answers one exec request per session, then closes the channel.
func (s *Server) session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	for req := range reqs {
		if req.Type != "exec" {
			_ = req.Reply(false, nil)
			continue
		}
		var p struct{ Command string }
		if err := ssh.Unmarshal(req.Payload, &p); err != nil {
			_ = req.Reply(false, nil)
			return
		}
		_ = req.Reply(true, nil)
		r, ok := s.cfg.Commands[p.Command]
		if !ok {
			r = Reply{Out: "unknown command: " + p.Command + "\n", Exit: 127}
		}
		time.Sleep(r.Delay)
		_, _ = io.WriteString(ch, r.Out)
		_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(r.Exit)}))
		return
	}
}

// NewSigner makes an ed25519 key.
func NewSigner(t testing.TB) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sg, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return sg
}

// NewRSASigner makes an RSA key, to show a host key of another type than ed25519.
func NewRSASigner(t testing.TB) ssh.Signer {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	sg, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return sg
}

// KeyPEM makes an ed25519 login key in the OpenSSH private key format, optionally protected by a passphrase, and returns it
// with its public key.
func KeyPEM(t testing.TB, passphrase string) (string, ssh.PublicKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	sg, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(block)), sg.PublicKey()
}
