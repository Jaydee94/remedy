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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Jaydee94/remedy/internal/secret"
)

const (
	apiVersion = "2022-11-28"
	userAgent  = "remedy"

	// PageSize is the page size of the list calls. A list that comes back full may be truncated.
	PageSize = 100

	// maxBody bounds a response. A full page of pull requests is a few megabytes of JSON.
	maxBody = 8 << 20

	// The ETag cache keeps response bodies so that a 304 can be answered from memory.
	maxCachedBody   = 2 << 20
	maxCacheEntries = 256

	defaultRateLimitWait = time.Minute
	maxRateLimitWait     = time.Hour
)

var (
	// ErrUnauthorized means GitHub rejected the token.
	ErrUnauthorized = errors.New("github: token rejected")
	// ErrNotFound means the resource does not exist or the token has no access to it. GitHub
	// answers 404 in both cases on purpose.
	ErrNotFound = errors.New("github: not found, or no access")
	// ErrInvalidRepoName means the name is not of the form owner/name.
	ErrInvalidRepoName = errors.New("github: repository name must be owner/name")
	// ErrInvalidRef means the commit SHA or branch name cannot be used in a request path.
	ErrInvalidRef = errors.New("github: invalid commit or branch name")
)

var (
	repoName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	refName  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
)

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

// validRef accepts a commit SHA or a branch name (which may contain slashes) that is safe to put
// into a request path.
func validRef(s string) bool {
	if !refName.MatchString(s) {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// APIError is any other non-success answer, for example a 403 for a missing permission or a 5xx.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("github: HTTP %d: %s", e.Status, e.Message) }

// RateLimitError means GitHub asked the client to slow down (primary or secondary rate limit).
type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("github: rate limited, retry in %s", e.RetryAfter.Round(time.Second))
}

type User struct {
	Login string `json:"login"`
}

type Repo struct {
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}

// PullRequest is the part of a pull request that Remedy reads.
type PullRequest struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Draft   bool   `json:"draft"`
	HTMLURL string `json:"html_url"`
	Head    struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
	User struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"user"`
}

// CheckRun is one check of a commit. Conclusion is empty until the run has completed.
type CheckRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HeadSHA    string `json:"head_sha"`
	HTMLURL    string `json:"html_url"`
}

type cacheEntry struct {
	etag string
	body []byte
}

// Client is a read-only GitHub API client. Every exported method is a Get or a List. A Client
// remembers the ETag and the body of its answers, so use one Client per token for as long as the
// token lives.
type Client struct {
	baseURL string
	token   secret.Value
	http    *http.Client

	mu    sync.Mutex
	cache map[string]cacheEntry // by request path and query
}

// New returns a client for baseURL (for example https://api.github.com). A nil httpClient gets a
// 20 second timeout.
func New(baseURL string, token secret.Value, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    httpClient,
		cache:   map[string]cacheEntry{},
	}
}

// scrub removes the token from text that may be shown to a user or written to a log.
func (c *Client) scrub(s string) string {
	if t := c.token.Reveal(); t != "" {
		s = strings.ReplaceAll(s, t, "***")
	}
	return s
}

func (c *Client) cached(path string) (cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.cache[path]
	return e, ok
}

func (c *Client) remember(path, etag string, body []byte) {
	if etag == "" || len(body) > maxCachedBody {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.cache) >= maxCacheEntries {
		clear(c.cache) // a simple bound; the next cycle refills what it needs
	}
	c.cache[path] = cacheEntry{etag: etag, body: body}
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
	cached, hasCached := c.cached(path)
	if hasCached {
		req.Header.Set("If-None-Match", cached.etag)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New(c.scrub(err.Error()))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))

	switch resp.StatusCode {
	case http.StatusOK:
		c.remember(path, resp.Header.Get("ETag"), body)
	case http.StatusNotModified:
		if !hasCached {
			return errors.New("github: unexpected 304 without a cached response")
		}
		body = cached.body
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusForbidden, http.StatusTooManyRequests:
		if wait, ok := rateLimitWait(resp.StatusCode, resp.Header); ok {
			return &RateLimitError{RetryAfter: wait}
		}
		return c.apiError(resp.StatusCode, body)
	default:
		return c.apiError(resp.StatusCode, body)
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("github: unexpected response: %w", err)
	}
	return nil
}

func (c *Client) apiError(status int, body []byte) error {
	msg := string(body)
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return &APIError{Status: status, Message: c.scrub(strings.TrimSpace(msg))}
}

// rateLimitWait reads how long GitHub wants the client to wait. ok is false when the answer is not
// a rate limit: a 403 can also mean a missing permission.
func rateLimitWait(status int, h http.Header) (wait time.Duration, ok bool) {
	if v := h.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
			return clampWait(time.Duration(secs) * time.Second), true
		}
	}
	if h.Get("X-Ratelimit-Remaining") == "0" {
		if reset, err := strconv.ParseInt(h.Get("X-Ratelimit-Reset"), 10, 64); err == nil {
			return clampWait(time.Until(time.Unix(reset, 0))), true
		}
		return defaultRateLimitWait, true
	}
	if status == http.StatusTooManyRequests {
		return defaultRateLimitWait, true
	}
	return 0, false
}

func clampWait(d time.Duration) time.Duration {
	return min(max(d, time.Second), maxRateLimitWait)
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

// ListOpenPRs returns the open pull requests of a repository, newest first, at most PageSize.
func (c *Client) ListOpenPRs(ctx context.Context, fullName string) ([]PullRequest, error) {
	if !validRepoName(fullName) {
		return nil, ErrInvalidRepoName
	}
	var prs []PullRequest
	if err := c.get(ctx, "/repos/"+fullName+"/pulls?state=open&per_page="+strconv.Itoa(PageSize), &prs); err != nil {
		return nil, err
	}
	return prs, nil
}

// ListCheckRuns returns the latest check run per check name for a commit SHA or a branch name, at
// most PageSize.
func (c *Client) ListCheckRuns(ctx context.Context, fullName, ref string) ([]CheckRun, error) {
	if !validRepoName(fullName) {
		return nil, ErrInvalidRepoName
	}
	if !validRef(ref) {
		return nil, ErrInvalidRef
	}
	var page struct {
		CheckRuns []CheckRun `json:"check_runs"`
	}
	path := "/repos/" + fullName + "/commits/" + ref + "/check-runs?filter=latest&per_page=" + strconv.Itoa(PageSize)
	if err := c.get(ctx, path, &page); err != nil {
		return nil, err
	}
	return page.CheckRuns, nil
}
