// Package github is a read-only client for the parts of the GitHub REST API that Remedy uses.
// It only ever sends GET requests, and its errors never contain the token.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/secret"
)

const (
	apiVersion = "2022-11-28"
	userAgent  = "remedy"
	maxBody    = 1 << 20
)

var (
	// ErrUnauthorized means GitHub rejected the token.
	ErrUnauthorized = errors.New("github: token rejected")
	// ErrNotFound means the resource does not exist or the token has no access to it. GitHub
	// answers 404 in both cases on purpose.
	ErrNotFound = errors.New("github: not found, or no access")
	// ErrInvalidRepoName means the name is not of the form owner/name.
	ErrInvalidRepoName = errors.New("github: repository name must be owner/name")
)

var repoName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// validRepoName accepts owner/name. The character class allows dots, so "." and ".." must be
// rejected explicitly: "../x" would otherwise turn into a request to a different path.
func validRepoName(s string) bool {
	if !repoName.MatchString(s) {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}

// APIError is any other non-success answer, for example a rate limit (403, 429) or a 5xx.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("github: HTTP %d: %s", e.Status, e.Message) }

type User struct {
	Login string `json:"login"`
}

type Repo struct {
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}

// Client is a read-only GitHub API client. Every exported method is a Get or a List.
type Client struct {
	baseURL string
	token   secret.Value
	http    *http.Client
}

// New returns a client for baseURL (for example https://api.github.com). A nil httpClient gets a
// 20 second timeout.
func New(baseURL string, token secret.Value, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), token: token, http: httpClient}
}

// scrub removes the token from text that may be shown to a user or written to a log.
func (c *Client) scrub(s string) string {
	if t := c.token.Reveal(); t != "" {
		s = strings.ReplaceAll(s, t, "***")
	}
	return s
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return errors.New(c.scrub(err.Error()))
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Authorization", "Bearer "+c.token.Reveal())

	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New(c.scrub(err.Error()))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))

	switch resp.StatusCode {
	case http.StatusOK:
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("github: unexpected response: %w", err)
		}
		return nil
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusNotFound:
		return ErrNotFound
	default:
		msg := string(body)
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return &APIError{Status: resp.StatusCode, Message: c.scrub(strings.TrimSpace(msg))}
	}
}

// GetUser returns the account the token belongs to. It is the cheapest way to check a token.
func (c *Client) GetUser(ctx context.Context) (User, error) {
	var u User
	if err := c.get(ctx, "/user", &u); err != nil {
		return User{}, err
	}
	if u.Login == "" {
		return User{}, errors.New("github: unexpected response: no login")
	}
	return u, nil
}

// GetRepo returns a repository by its full name (owner/name).
func (c *Client) GetRepo(ctx context.Context, fullName string) (Repo, error) {
	if !validRepoName(fullName) {
		return Repo{}, ErrInvalidRepoName
	}
	var r Repo
	if err := c.get(ctx, "/repos/"+fullName, &r); err != nil {
		return Repo{}, err
	}
	return r, nil
}
