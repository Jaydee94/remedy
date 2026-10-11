package sshhost

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
)

// MaxOutput is the most bytes of one command's output that are kept.
const MaxOutput = 16 << 10

var (
	connectTimeout = 10 * time.Second
	commandTimeout = 10 * time.Second
)

// The closed set of ways a connection fails. A raw error from the library is never passed on: it can contain the user name and
// the address.
var (
	ErrUnreachable    = errors.New("the host cannot be reached")
	ErrRefused        = errors.New("the host refused the connection")
	ErrTimeout        = errors.New("the host did not answer in time")
	ErrAuth           = errors.New("the login was rejected")
	ErrHostKeyChanged = errors.New("the host key is not the pinned one")
	ErrNoPin          = errors.New("the host key is not pinned")
)

var errProbeDone = errors.New("probe: key received")

type Target struct {
	Address string
	Port    int
	Account string
}

// HostKey is a host key as it can be shown and pinned.
type HostKey struct {
	Type        string
	Fingerprint string
	Line        string // authorized_keys format; what is stored as the pin
}

func hostKeyOf(k ssh.PublicKey) HostKey {
	return HostKey{Type: k.Type(), Fingerprint: ssh.FingerprintSHA256(k), Line: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))}
}

func parsePin(line string) (ssh.PublicKey, error) {
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	return pub, err
}

// FingerprintOfLine is the SHA-256 fingerprint of a pinned key line.
func FingerprintOfLine(line string) (string, error) {
	pub, err := parsePin(line)
	if err != nil {
		return "", err
	}
	return ssh.FingerprintSHA256(pub), nil
}

// algorithmsFor limits the host key algorithms to the pinned key's type, so that a server which offers several cannot make a
// correct pin fail, and a key of another type shows up as a changed key.
func algorithmsFor(pub ssh.PublicKey) []string {
	if pub.Type() == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}
	}
	return []string{pub.Type()}
}

func classify(err error) error {
	var ne net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return ErrTimeout
	case errors.Is(err, syscall.ECONNREFUSED):
		return ErrRefused
	case strings.Contains(err.Error(), "unable to authenticate"):
		return ErrAuth
	case strings.Contains(err.Error(), "no common algorithm for host key"):
		return ErrHostKeyChanged
	}
	return ErrUnreachable
}

func connect(ctx context.Context, t Target, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	d := net.Dialer{Timeout: connectTimeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(t.Address, strconv.Itoa(t.Port)))
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(connectTimeout))
	c, chans, reqs, err := ssh.NewClientConn(conn, conn.RemoteAddr().String(), cfg)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return ssh.NewClient(c, chans, reqs), nil
}

// Probe connects for the key exchange only and returns the key the host presents. It sends no user name and no login.
func Probe(ctx context.Context, t Target) (HostKey, error) {
	var got ssh.PublicKey
	cfg := &ssh.ClientConfig{
		User: t.Account,
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			got = k
			return errProbeDone
		},
		Timeout: connectTimeout,
	}
	c, err := connect(ctx, t, cfg)
	if c != nil {
		_ = c.Close()
	}
	if got != nil {
		return hostKeyOf(got), nil
	}
	if err == nil {
		return HostKey{}, ErrUnreachable
	}
	return HostKey{}, classify(err)
}

// Client is a connection to a host whose key matched the pin.
type Client struct{ c *ssh.Client }

// Dial connects and logs in. It never offers a login to a host that does not present exactly the pinned key.
func Dial(ctx context.Context, t Target, cred Credential, pinLine string) (*Client, error) {
	if pinLine == "" {
		return nil, ErrNoPin
	}
	pin, err := parsePin(pinLine)
	if err != nil {
		return nil, ErrNoPin
	}
	methods, err := cred.methods()
	if err != nil {
		return nil, err
	}
	mismatch := false
	cfg := &ssh.ClientConfig{
		User:              t.Account,
		Auth:              methods,
		HostKeyAlgorithms: algorithmsFor(pin),
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			if bytes.Equal(k.Marshal(), pin.Marshal()) {
				return nil
			}
			mismatch = true
			return errors.New("host key mismatch")
		},
		Timeout: connectTimeout,
	}
	c, err := connect(ctx, t, cfg)
	if err != nil {
		if mismatch {
			return nil, ErrHostKeyChanged
		}
		return nil, classify(err)
	}
	return &Client{c: c}, nil
}

func (c *Client) Close() error { return c.c.Close() }

// Result is the answer of one command.
type Result struct {
	Output    string
	ExitCode  int
	Truncated bool
}

// capWriter keeps the first max bytes and notes that it dropped the rest. It is safe for the two streams of a session.
type capWriter struct {
	mu  sync.Mutex
	buf []byte
	max int
	cut bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	room := w.max - len(w.buf)
	if room > 0 {
		w.buf = append(w.buf, p[:min(room, len(p))]...)
	}
	if len(p) > room {
		w.cut = true
	}
	return len(p), nil
}

func (w *capWriter) result() (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.buf), w.cut
}

// Run runs one command in its own session: no pty, no environment, no shell of ours. cmd must be built from constants.
func (c *Client) Run(ctx context.Context, cmd string) (Result, error) {
	sess, err := c.c.NewSession()
	if err != nil {
		return Result{}, ErrUnreachable
	}
	defer sess.Close()
	w := &capWriter{max: MaxOutput}
	// Only stdout is kept: a notice on stderr such as "sudo: unable to resolve host" must not become part of `id -un`.
	sess.Stdout, sess.Stderr = w, io.Discard
	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()
	timer := time.NewTimer(commandTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		out, cut := w.result()
		res := Result{Output: out, Truncated: cut}
		var ee *ssh.ExitError
		switch {
		case errors.As(err, &ee):
			res.ExitCode = ee.ExitStatus()
		case err != nil:
			return res, ErrUnreachable
		}
		return res, nil
	case <-timer.C:
		return Result{}, ErrTimeout
	case <-ctx.Done():
		return Result{}, ErrTimeout
	}
}
