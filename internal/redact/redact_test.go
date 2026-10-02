package redact_test

import (
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/redact"
)

func TestRedactRemovesSecrets(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"classic GitHub token", "token ghp_0123456789abcdefghijABCDEFGHIJ0123 used", "token [REDACTED:github-token] used"},
		{"server token", "ghs_0123456789abcdefghijABCDEFGHIJ0123", "[REDACTED:github-token]"},
		{"fine-grained token", "github_pat_11ABCDEFG0123456789_abcdefghijklmnopqrstuvwxyz0123456789", "[REDACTED:github-token]"},
		{"AWS access key id", "key AKIAIOSFODNN7EXAMPLE here", "key [REDACTED:aws-key] here"},
		{"bearer header", "Authorization: Bearer abc123.def456-ghi789", "Authorization: Bearer [REDACTED]"},
		{"bearer in lower case", "authorization: bearer abcdefgh12345", "authorization: bearer [REDACTED]"},
		{"password assignment", "password=hunter2", "password=[REDACTED]"},
		{"password with a colon", "PASSWORD: s3cret!", "PASSWORD: [REDACTED]"},
		{"quoted value with spaces", `db_password = "correct horse battery"`, `db_password = [REDACTED]`},
		{"JSON member", `{"password": "hunter2", "user": "octo"}`, `{"password": [REDACTED], "user": "octo"}`},
		{"secret in an environment name", "AWS_SECRET_ACCESS_KEY=abc/def+ghi", "AWS_SECRET_ACCESS_KEY=[REDACTED]"},
		{"masked value still goes", "GITHUB_TOKEN: ***", "GITHUB_TOKEN: [REDACTED]"},
		{"api key", "api_key: 0a1b2c3d4e5f", "api_key: [REDACTED]"},
		{"credentials in a URL", "git clone https://octo:s3cr3t@github.com/octo/hello.git", "git clone https://[REDACTED]@github.com/octo/hello.git"},
		{"JWT", "jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk end", "jwt [REDACTED:jwt] end"},
	}
	for _, tc := range cases {
		if got := redact.Redact(tc.in); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

func TestRedactRemovesAPrivateKeyBlock(t *testing.T) {
	in := "before\n-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\nabcdef\n-----END RSA PRIVATE KEY-----\nafter"
	want := "before\n[REDACTED:private-key]\nafter"
	if got := redact.Redact(in); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRedactRemovesAKeyBlockThatWasCutOff(t *testing.T) {
	in := "log line\n-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\nmore key material"
	got := redact.Redact(in)
	if got != "log line\n[REDACTED:private-key]" {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "b3BlbnNz") {
		t.Fatal("key material survived")
	}
}

func TestRedactLeavesOrdinaryTextAlone(t *testing.T) {
	for _, in := range []string{
		"Run npm ci",
		"npm error code EUSAGE",
		"npm error Invalid: lock file's typescript@6.0.3 does not satisfy typescript@7.0.2",
		"the token was rejected by the server",
		"commit 913da1edbf28ced7b324b5b99ab3c6c61241acee",
		"ghp_short",
		"AKIA is a prefix",
		"/home/runner/work/_temp/git-credentials-c70c5ad6.config",
		"Bearer",
		"password",
		"web/package.json | 2 +-",
		"",
	} {
		if got := redact.Redact(in); got != in {
			t.Errorf("changed %q into %q", in, got)
		}
	}
}

func TestRedactIsIdempotent(t *testing.T) {
	in := "ghp_0123456789abcdefghijABCDEFGHIJ0123 password=x Bearer abcdefgh1234 https://a:b@c.de/ AKIAIOSFODNN7EXAMPLE"
	once := redact.Redact(in)
	if twice := redact.Redact(once); twice != once {
		t.Fatalf("not idempotent:\n once %q\ntwice %q", once, twice)
	}
}

func TestRedactHandlesAHugeInputQuickly(t *testing.T) {
	in := strings.Repeat("2026-10-02T12:16:39.4486Z npm error some line of a log with no secret in it at all\n", 20000)
	if got := redact.Redact(in); got != in {
		t.Fatal("a log without secrets was changed")
	}
}
