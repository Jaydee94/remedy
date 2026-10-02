package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/github"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/store"
)

// GitHub is what the control plane needs from the GitHub API. *github.Client implements it.
type GitHub interface {
	GetUser(ctx context.Context) (github.User, error)
	GetRepo(ctx context.Context, fullName string) (github.Repo, error)
}

const (
	minTokenLen = 20
	maxTokenLen = 512

	undecryptableDetail = "The stored token cannot be decrypted. Check REMEDY_MASTER_KEY or enter the token again."
)

// connectionAAD binds the sealed token to its row, so a ciphertext cannot be moved to another one.
func connectionAAD() string { return "github_connection:" + strconv.FormatInt(store.ConnectionID, 10) }

type connectionView struct {
	Connected    bool       `json:"connected"`
	Login        string     `json:"login,omitempty"`
	TokenHint    string     `json:"tokenHint,omitempty"`
	Status       string     `json:"status,omitempty"`
	StatusDetail string     `json:"statusDetail,omitempty"`
	CheckedAt    *time.Time `json:"checkedAt,omitempty"`
}

// viewOf never contains the token, only the last four characters.
func viewOf(c store.Connection) connectionView {
	at := c.CheckedAt
	return connectionView{
		Connected:    true,
		Login:        c.Login,
		TokenHint:    "…" + c.TokenHint,
		Status:       string(c.Status),
		StatusDetail: c.StatusDetail,
		CheckedAt:    &at,
	}
}

func (s *srv) openToken(c store.Connection) (secret.Value, error) {
	raw, err := s.d.Key.Open(c.TokenCiphertext, connectionAAD())
	if err != nil {
		return secret.Value{}, err
	}
	return secret.NewValue(string(raw)), nil
}

func (s *srv) markUndecryptable(ctx context.Context) {
	_ = s.d.Store.UpdateConnectionStatus(ctx, store.ConnUndecryptable, undecryptableDetail, time.Now())
}

func (s *srv) getConnection(w http.ResponseWriter, r *http.Request) {
	c, err := s.d.Store.GetConnection(r.Context())
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, connectionView{Connected: false})
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the connection")
		return
	}
	writeJSON(w, http.StatusOK, viewOf(c))
}

func (s *srv) putConnection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	token := strings.TrimSpace(req.Token)
	if len(token) < minTokenLen || len(token) > maxTokenLen {
		writeErr(w, http.StatusBadRequest, "the token must be 20 to 512 characters")
		return
	}

	user, err := s.d.NewGitHub(secret.NewValue(token)).GetUser(r.Context())
	if errors.Is(err, github.ErrUnauthorized) {
		writeErr(w, http.StatusBadRequest, "GitHub rejected the token")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, "could not reach GitHub")
		return
	}

	sealed, err := s.d.Key.Seal([]byte(token), connectionAAD())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not store the token")
		return
	}
	conn := store.Connection{
		TokenCiphertext: sealed,
		TokenHint:       token[len(token)-4:],
		Login:           user.Login,
		Status:          store.ConnOK,
		CheckedAt:       time.Now().UTC(), // UTC, like every time that comes back from the store
	}
	if err := s.d.Store.SaveConnection(r.Context(), conn); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not store the connection")
		return
	}
	writeJSON(w, http.StatusOK, viewOf(conn))
}

// checkConnection re-validates the stored token and records the result.
func (s *srv) checkConnection(w http.ResponseWriter, r *http.Request) {
	conn, err := s.d.Store.GetConnection(r.Context())
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no GitHub connection")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the connection")
		return
	}

	token, err := s.openToken(conn)
	switch {
	case errors.Is(err, secret.ErrOpen):
		s.markUndecryptable(r.Context())
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "could not read the token")
		return
	default:
		status, detail := store.ConnOK, ""
		if _, err := s.d.NewGitHub(token).GetUser(r.Context()); errors.Is(err, github.ErrUnauthorized) {
			status, detail = store.ConnError, "GitHub rejected the stored token."
		} else if err != nil {
			status, detail = store.ConnError, "Could not reach GitHub."
		}
		_ = s.d.Store.UpdateConnectionStatus(r.Context(), status, detail, time.Now())
	}

	updated, err := s.d.Store.GetConnection(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the connection")
		return
	}
	writeJSON(w, http.StatusOK, viewOf(updated))
}

func (s *srv) deleteConnection(w http.ResponseWriter, r *http.Request) {
	err := s.d.Store.DeleteConnection(r.Context())
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no GitHub connection")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not remove the connection")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type repoView struct {
	ID            int64      `json:"id"`
	FullName      string     `json:"fullName"`
	DefaultBranch string     `json:"defaultBranch"`
	Enabled       bool       `json:"enabled"`
	LastPolledAt  *time.Time `json:"lastPolledAt,omitempty"`
	LastError     string     `json:"lastError"`
	CreatedAt     time.Time  `json:"createdAt"`
}

func repoViewOf(r store.Repo) repoView {
	return repoView{
		ID: r.ID, FullName: r.FullName, DefaultBranch: r.DefaultBranch, Enabled: r.Enabled,
		LastPolledAt: r.LastPolledAt, LastError: r.LastError, CreatedAt: r.CreatedAt,
	}
}

func (s *srv) listRepos(w http.ResponseWriter, r *http.Request) {
	repos, err := s.d.Store.ListRepos(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not list repos")
		return
	}
	views := make([]repoView, 0, len(repos))
	for _, repo := range repos {
		views = append(views, repoViewOf(repo))
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *srv) addRepo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FullName string `json:"fullName"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}

	conn, err := s.d.Store.GetConnection(r.Context())
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusConflict, "connect GitHub first")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the connection")
		return
	}
	token, err := s.openToken(conn)
	if errors.Is(err, secret.ErrOpen) {
		s.markUndecryptable(r.Context())
		writeErr(w, http.StatusConflict, undecryptableDetail)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not read the token")
		return
	}

	repo, err := s.d.NewGitHub(token).GetRepo(r.Context(), strings.TrimSpace(req.FullName))
	switch {
	case errors.Is(err, github.ErrInvalidRepoName):
		writeErr(w, http.StatusBadRequest, "use the form owner/name")
		return
	case errors.Is(err, github.ErrNotFound):
		writeErr(w, http.StatusBadRequest, "repository not found, or the token has no access to it")
		return
	case errors.Is(err, github.ErrUnauthorized):
		writeErr(w, http.StatusBadRequest, "GitHub rejected the stored token")
		return
	case err != nil:
		writeErr(w, http.StatusBadGateway, "could not reach GitHub")
		return
	}

	added, err := s.d.Store.AddRepo(r.Context(), conn.ID, repo.FullName, repo.DefaultBranch)
	if errors.Is(err, store.ErrExists) {
		writeErr(w, http.StatusConflict, "this repository is already added")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not add the repository")
		return
	}
	writeJSON(w, http.StatusCreated, repoViewOf(added))
}

// repoID parses the {id} path value. ok is false for anything that is not a number.
func repoID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil
}

func (s *srv) patchRepo(w http.ResponseWriter, r *http.Request) {
	id, ok := repoID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "repository not found")
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Enabled == nil {
		writeErr(w, http.StatusBadRequest, "enabled is required")
		return
	}
	err := s.d.Store.SetRepoEnabled(r.Context(), id, *req.Enabled)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "repository not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not update the repository")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) deleteRepo(w http.ResponseWriter, r *http.Request) {
	id, ok := repoID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "repository not found")
		return
	}
	err := s.d.Store.DeleteRepo(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "repository not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not remove the repository")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
