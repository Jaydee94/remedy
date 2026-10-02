package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// MaxLogBytes bounds the download of a job log.
const MaxLogBytes = 16 << 20

// PRFile is one file of a pull request. Patch is empty for binary or very large files.
type PRFile struct {
	Filename  string `json:"filename"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Patch     string `json:"patch"`
}

// statusError maps a failed answer to the client's errors.
func (c *Client) statusError(status int, h http.Header, body []byte) error {
	switch status {
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusForbidden, http.StatusTooManyRequests:
		if wait, ok := rateLimitWait(status, h); ok {
			return &RateLimitError{RetryAfter: wait}
		}
	}
	return c.apiError(status, body)
}

// open sends a GET and returns the answer when it is a 200; anything else is an error and the body is
// closed. The caller closes the body of a returned response.
//
// Job logs and archives are served from another host after a redirect. Go does not forward the
// Authorization header to a different host, and the error of a failed hop is reduced to its cause: the
// URL of the second hop is a signed link and must not reach a log.
func (c *Client) open(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, errors.New(c.scrub(err.Error()))
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Authorization", "Bearer "+c.token.Reveal())

	resp, err := c.stream.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, errors.New(c.scrub(err.Error()))
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		return nil, c.statusError(resp.StatusCode, resp.Header, body)
	}
	return resp, nil
}

// GetPR returns one pull request, including its description.
func (c *Client) GetPR(ctx context.Context, fullName string, number int) (PullRequest, error) {
	if !validRepoName(fullName) {
		return PullRequest{}, ErrInvalidRepoName
	}
	if number <= 0 {
		return PullRequest{}, fmt.Errorf("github: invalid pull request number %d", number)
	}
	var pr PullRequest
	if err := c.get(ctx, "/repos/"+fullName+"/pulls/"+strconv.Itoa(number), &pr); err != nil {
		return PullRequest{}, err
	}
	return pr, nil
}

// ListPRFiles returns the files a pull request changes, at most PageSize.
func (c *Client) ListPRFiles(ctx context.Context, fullName string, number int) ([]PRFile, error) {
	if !validRepoName(fullName) {
		return nil, ErrInvalidRepoName
	}
	if number <= 0 {
		return nil, fmt.Errorf("github: invalid pull request number %d", number)
	}
	var files []PRFile
	path := "/repos/" + fullName + "/pulls/" + strconv.Itoa(number) + "/files?per_page=" + strconv.Itoa(PageSize)
	if err := c.get(ctx, path, &files); err != nil {
		return nil, err
	}
	return files, nil
}

// GetJobLogs returns the log of a GitHub Actions job (the id of its check run). truncated is true when
// the log is longer than MaxLogBytes; the end is what is cut off then. ErrNotFound means the check has no
// log (it is not an Actions job); an expired log is an *APIError with status 410.
func (c *Client) GetJobLogs(ctx context.Context, fullName string, jobID int64) (text string, truncated bool, err error) {
	if !validRepoName(fullName) {
		return "", false, ErrInvalidRepoName
	}
	if jobID <= 0 {
		return "", false, fmt.Errorf("github: invalid job id %d", jobID)
	}
	resp, err := c.open(ctx, "/repos/"+fullName+"/actions/jobs/"+strconv.FormatInt(jobID, 10)+"/logs")
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxLogBytes+1))
	if err != nil {
		return "", false, errors.New(c.scrub(err.Error()))
	}
	if len(b) > MaxLogBytes {
		b, truncated = b[:MaxLogBytes], true
	}
	return string(b), truncated, nil
}

// GetTarball streams the repository at a commit as a gzipped tar archive. The caller closes the result.
func (c *Client) GetTarball(ctx context.Context, fullName, ref string) (io.ReadCloser, error) {
	if !validRepoName(fullName) {
		return nil, ErrInvalidRepoName
	}
	if !validRef(ref) {
		return nil, ErrInvalidRef
	}
	resp, err := c.open(ctx, "/repos/"+fullName+"/tarball/"+ref)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}
