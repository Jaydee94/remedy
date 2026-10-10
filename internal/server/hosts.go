package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/sshhost"
	"github.com/Jaydee94/remedy/internal/store"
)

const (
	maxHostBody       = 16 << 10
	undecryptableHost = "The stored login cannot be decrypted. Check REMEDY_MASTER_KEY or enter the login again."
)

type credentialView struct {
	Kind      string    `json:"kind"`
	Hint      string    `json:"hint"`
	PublicKey string    `json:"publicKey,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type onboardingView struct {
	AuthorizedKeys string `json:"authorizedKeys"`
	Sudoers        string `json:"sudoers"`
	Error          string `json:"error,omitempty"`
}

// hostView has no field that could carry a secret: a credential shows its kind, a hint and a public key.
type hostView struct {
	ID                int64           `json:"id"`
	Name              string          `json:"name"`
	Address           string          `json:"address"`
	Port              int             `json:"port"`
	Account           string          `json:"account"`
	Notes             string          `json:"notes"`
	DockerPattern     string          `json:"dockerPattern"`
	SystemdUnits      []string        `json:"systemdUnits"`
	Enabled           bool            `json:"enabled"`
	Status            string          `json:"status"`
	StatusDetail      string          `json:"statusDetail"`
	CheckedAt         *time.Time      `json:"checkedAt,omitempty"`
	Report            *sshhost.Report `json:"report,omitempty"`
	PinnedFingerprint string          `json:"pinnedFingerprint,omitempty"`
	PinnedAt          *time.Time      `json:"pinnedAt,omitempty"`
	Credential        *credentialView `json:"credential,omitempty"`
	Onboarding        *onboardingView `json:"onboarding,omitempty"`
	CreatedAt         time.Time       `json:"createdAt"`
	UpdatedAt         time.Time       `json:"updatedAt"`
}

type presentedKey struct {
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
}

type testView struct {
	hostView
	PresentedKey *presentedKey `json:"presentedKey,omitempty"`
}

type patchView struct {
	hostView
	PinCleared bool `json:"pinCleared"`
}

// hostViewOf builds the view of a host. The detail view also carries the report and the onboarding snippet. (The name viewOf is
// taken by the GitHub connection's view.)
func hostViewOf(e store.HostEntry, detail bool) hostView {
	h := e.Host
	v := hostView{
		ID: h.ID, Name: h.Name, Address: h.Address, Port: h.Port, Account: h.Account, Notes: h.Notes, DockerPattern: h.DockerPattern,
		SystemdUnits: h.SystemdUnits, Enabled: h.Enabled, Status: string(h.Status), StatusDetail: h.StatusDetail,
		CheckedAt: h.CheckedAt, PinnedAt: h.PinnedAt, CreatedAt: h.CreatedAt, UpdatedAt: h.UpdatedAt,
	}
	if v.SystemdUnits == nil {
		v.SystemdUnits = []string{}
	}
	if h.PinnedKey != "" {
		if fp, err := sshhost.FingerprintOfLine(h.PinnedKey); err == nil {
			v.PinnedFingerprint = fp
		}
	}
	if c := e.Credential; c != nil {
		v.Credential = &credentialView{Kind: c.Kind, Hint: c.Hint, PublicKey: c.PublicKey, UpdatedAt: c.UpdatedAt}
	}
	if !detail {
		return v
	}
	if h.Report != "" {
		var rep sshhost.Report
		if json.Unmarshal([]byte(h.Report), &rep) == nil {
			v.Report = &rep
		}
	}
	in := sshhost.SnippetInput{Account: h.Account, DockerPattern: h.DockerPattern, SystemdUnits: h.SystemdUnits}
	if e.Credential != nil {
		in.AuthorizedKey = e.Credential.PublicKey
	}
	if out, err := sshhost.Snippet(in); err != nil {
		v.Onboarding = &onboardingView{Error: "I cannot make a snippet from these fields."}
	} else {
		v.Onboarding = &onboardingView{AuthorizedKeys: out.AuthorizedKeys, Sudoers: out.Sudoers}
	}
	return v
}

func hostID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil
}

// hostBody is the body of a create or a patch. A field that is absent keeps its value.
type hostBody struct {
	Name          *string   `json:"name"`
	Address       *string   `json:"address"`
	Port          *int      `json:"port"`
	Account       *string   `json:"account"`
	Notes         *string   `json:"notes"`
	DockerPattern *string   `json:"dockerPattern"`
	SystemdUnits  *[]string `json:"systemdUnits"`
	Enabled       *bool     `json:"enabled"`
}

func (b hostBody) apply(f store.HostFields) store.HostFields {
	if b.Name != nil {
		f.Name = strings.TrimSpace(*b.Name)
	}
	if b.Address != nil {
		f.Address = strings.TrimSpace(*b.Address)
	}
	if b.Port != nil {
		f.Port = *b.Port
	}
	if b.Account != nil {
		f.Account = strings.TrimSpace(*b.Account)
	}
	if b.Notes != nil {
		f.Notes = *b.Notes
	}
	if b.DockerPattern != nil {
		f.DockerPattern = strings.TrimSpace(*b.DockerPattern)
	}
	if b.SystemdUnits != nil {
		f.SystemdUnits = *b.SystemdUnits
	}
	if b.Enabled != nil {
		f.Enabled = *b.Enabled
	}
	return f
}

// checked validates the fields and returns them with the units normalised.
func checked(f store.HostFields) (store.HostFields, error) {
	v := sshhost.Fields{Name: f.Name, Address: f.Address, Port: f.Port, Account: f.Account, Notes: f.Notes, DockerPattern: f.DockerPattern, SystemdUnits: f.SystemdUnits}
	if err := v.Validate(); err != nil {
		return f, err
	}
	f.SystemdUnits = v.SystemdUnits
	return f, nil
}

func (s *srv) badFields(w http.ResponseWriter, err error) {
	var fe *sshhost.FieldError
	if errors.As(err, &fe) {
		writeErr(w, http.StatusBadRequest, fe.Msg)
		return
	}
	writeErr(w, http.StatusBadRequest, "invalid request body")
}

func readHostBody(w http.ResponseWriter, r *http.Request) (hostBody, bool) {
	var b hostBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxHostBody)).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return b, false
	}
	return b, true
}

func (s *srv) listHosts(w http.ResponseWriter, r *http.Request) {
	entries, err := s.d.Store.ListHosts(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not list hosts")
		return
	}
	views := make([]hostView, 0, len(entries))
	for _, e := range entries {
		views = append(views, hostViewOf(e, false))
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *srv) createHost(w http.ResponseWriter, r *http.Request) {
	body, ok := readHostBody(w, r)
	if !ok {
		return
	}
	fields, err := checked(body.apply(store.HostFields{Port: 22, Account: "remedy", Enabled: true}))
	if err != nil {
		s.badFields(w, err)
		return
	}
	h, err := s.d.Store.CreateHost(r.Context(), fields)
	if errors.Is(err, store.ErrExists) {
		writeErr(w, http.StatusConflict, "a host with this name already exists")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not create the host")
		return
	}
	writeJSON(w, http.StatusCreated, hostViewOf(store.HostEntry{Host: h}, false))
}

// load answers the request itself when the host does not exist.
func (s *srv) loadHost(w http.ResponseWriter, r *http.Request) (store.HostEntry, bool) {
	id, ok := hostID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "host not found")
		return store.HostEntry{}, false
	}
	e, err := s.d.Store.GetHost(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "host not found")
		return store.HostEntry{}, false
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the host")
		return store.HostEntry{}, false
	}
	return e, true
}

func (s *srv) getHost(w http.ResponseWriter, r *http.Request) {
	if e, ok := s.loadHost(w, r); ok {
		writeJSON(w, http.StatusOK, hostViewOf(e, true))
	}
}

func (s *srv) patchHost(w http.ResponseWriter, r *http.Request) {
	e, ok := s.loadHost(w, r)
	if !ok {
		return
	}
	body, ok := readHostBody(w, r)
	if !ok {
		return
	}
	fields, err := checked(body.apply(e.Host.HostFields))
	if err != nil {
		s.badFields(w, err)
		return
	}
	_, cleared, err := s.d.Store.UpdateHost(r.Context(), e.Host.ID, fields)
	if errors.Is(err, store.ErrExists) {
		writeErr(w, http.StatusConflict, "a host with this name already exists")
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "host not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not update the host")
		return
	}
	updated, err := s.d.Store.GetHost(r.Context(), e.Host.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the host")
		return
	}
	writeJSON(w, http.StatusOK, patchView{hostView: hostViewOf(updated, true), PinCleared: cleared})
}

func (s *srv) deleteHost(w http.ResponseWriter, r *http.Request) {
	e, ok := s.loadHost(w, r)
	if !ok {
		return
	}
	err := s.d.Store.DeleteHost(r.Context(), e.Host.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "host not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not remove the host")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) putHostCredential(w http.ResponseWriter, r *http.Request) {
	e, ok := s.loadHost(w, r)
	if !ok {
		return
	}
	var req struct {
		Kind       string `json:"kind"`
		Key        string `json:"key"`
		Passphrase string `json:"passphrase"`
		Password   string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxHostBody)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var (
		cred sshhost.Credential
		hint string
		pub  string
		err  error
	)
	switch req.Kind {
	case sshhost.KindKey:
		var info sshhost.KeyInfo
		cred, info, err = sshhost.NewKeyCredential(req.Key, req.Passphrase)
		hint, pub = info.Fingerprint, info.PublicLine
	case sshhost.KindPassword:
		cred, err = sshhost.NewPasswordCredential(req.Password)
		hint = "set"
	default:
		writeErr(w, http.StatusBadRequest, "the kind must be key or password")
		return
	}
	switch {
	case errors.Is(err, sshhost.ErrKeyFormat):
		writeErr(w, http.StatusBadRequest, "That is not a private key I can read. Paste the whole file, including the BEGIN and END lines.")
		return
	case errors.Is(err, sshhost.ErrPassphraseNeeded):
		writeErr(w, http.StatusBadRequest, "The key is protected by a passphrase. Enter it too.")
		return
	case errors.Is(err, sshhost.ErrPassphraseWrong):
		writeErr(w, http.StatusBadRequest, "The passphrase does not open the key.")
		return
	case errors.Is(err, sshhost.ErrPasswordInvalid):
		writeErr(w, http.StatusBadRequest, "The password must have 1 to 256 characters.")
		return
	case err != nil:
		writeErr(w, http.StatusBadRequest, "invalid login")
		return
	}

	blob, err := cred.Marshal()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not store the login")
		return
	}
	sealed, err := s.d.Key.Seal(blob, store.HostCredentialAAD(e.Host.ID))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not store the login")
		return
	}
	err = s.d.Store.SaveHostCredential(r.Context(), store.HostCredential{HostID: e.Host.ID, Kind: cred.Kind, Ciphertext: sealed, Hint: hint, PublicKey: pub})
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "host not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not store the login")
		return
	}
	updated, err := s.d.Store.GetHost(r.Context(), e.Host.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the host")
		return
	}
	writeJSON(w, http.StatusOK, hostViewOf(updated, true))
}

func (s *srv) deleteHostCredential(w http.ResponseWriter, r *http.Request) {
	e, ok := s.loadHost(w, r)
	if !ok {
		return
	}
	err := s.d.Store.DeleteHostCredential(r.Context(), e.Host.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "this host has no login")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not remove the login")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func targetOf(h store.Host) sshhost.Target {
	return sshhost.Target{Address: h.Address, Port: h.Port, Account: h.Account}
}

// hostFailure turns a failed connection into the status and the sentence the page shows. The texts are fixed: nothing the
// library or the host said is passed on.
func hostFailure(err error) (store.HostStatus, string) {
	switch {
	case errors.Is(err, sshhost.ErrHostKeyChanged):
		return store.HostKeyChanged, "The host shows another key than the pinned one. Find out why before you pin it again."
	case errors.Is(err, sshhost.ErrAuth):
		return store.HostError, "The host rejected the login."
	case errors.Is(err, sshhost.ErrTimeout):
		return store.HostError, "The host did not answer in time."
	case errors.Is(err, sshhost.ErrRefused):
		return store.HostError, "The host refused the connection."
	case errors.Is(err, sshhost.ErrBadCredential):
		return store.HostError, "The stored login cannot be used. Enter it again."
	}
	return store.HostError, "I could not reach the host."
}

func (s *srv) openHostCredential(e store.HostEntry) (sshhost.Credential, error) {
	raw, err := s.d.Key.Open(e.Credential.Ciphertext, store.HostCredentialAAD(e.Host.ID))
	if err != nil {
		return sshhost.Credential{}, err
	}
	return sshhost.ParseCredential(raw)
}

func (s *srv) testHost(w http.ResponseWriter, r *http.Request) {
	e, ok := s.loadHost(w, r)
	if !ok {
		return
	}
	if e.Credential == nil {
		writeErr(w, http.StatusConflict, "Enter a login first.")
		return
	}
	ctx, h := r.Context(), e.Host
	var presented *presentedKey

	if h.PinnedKey == "" {
		// Not pinned: only the key exchange. The login never leaves the control plane.
		key, err := s.d.Hosts.Probe(ctx, h.ID, targetOf(h))
		if err != nil {
			status, detail := hostFailure(err)
			_ = s.d.Store.SaveHostTest(ctx, h.ID, status, detail, "", time.Now())
		} else {
			presented = &presentedKey{Type: key.Type, Fingerprint: key.Fingerprint}
			_ = s.d.Store.SaveHostTest(ctx, h.ID, store.HostUntrusted, "", "", time.Now())
		}
	} else {
		cred, err := s.openHostCredential(e)
		switch {
		case errors.Is(err, secret.ErrOpen):
			_ = s.d.Store.SaveHostTest(ctx, h.ID, store.HostUndecryptable, undecryptableHost, "", time.Now())
		case err != nil:
			status, detail := hostFailure(err)
			_ = s.d.Store.SaveHostTest(ctx, h.ID, status, detail, "", time.Now())
		default:
			rep, err := s.d.Hosts.Check(ctx, h.ID, targetOf(h), cred, h.PinnedKey)
			if err != nil {
				status, detail := hostFailure(err)
				_ = s.d.Store.SaveHostTest(ctx, h.ID, status, detail, "", time.Now())
			} else if b, merr := json.Marshal(rep); merr == nil {
				_ = s.d.Store.SaveHostTest(ctx, h.ID, store.HostOK, "", string(b), time.Now())
			}
		}
	}

	updated, err := s.d.Store.GetHost(ctx, h.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "host not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the host")
		return
	}
	writeJSON(w, http.StatusOK, testView{hostView: hostViewOf(updated, true), PresentedKey: presented})
}

// pinHostKey pins the key the host shows now, but only if it is the one whose fingerprint the maintainer names. The server keeps
// no "pending" key: the call is the maintainer's statement of what they saw, checked against what the host presents now.
func (s *srv) pinHostKey(w http.ResponseWriter, r *http.Request) {
	e, ok := s.loadHost(w, r)
	if !ok {
		return
	}
	var req struct {
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Fingerprint == "" {
		writeErr(w, http.StatusBadRequest, "the fingerprint is required")
		return
	}
	key, err := s.d.Hosts.Probe(r.Context(), e.Host.ID, targetOf(e.Host))
	if err != nil {
		_, detail := hostFailure(err)
		writeErr(w, http.StatusBadGateway, detail)
		return
	}
	if key.Fingerprint != req.Fingerprint {
		writeErr(w, http.StatusConflict, "The host shows another key now than the one you checked. Test the connection again.")
		return
	}
	if err := s.d.Store.PinHostKey(r.Context(), e.Host.ID, key.Line, time.Now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "host not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "could not pin the key")
		return
	}
	updated, err := s.d.Store.GetHost(r.Context(), e.Host.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the host")
		return
	}
	writeJSON(w, http.StatusOK, hostViewOf(updated, true))
}

func (s *srv) unpinHostKey(w http.ResponseWriter, r *http.Request) {
	e, ok := s.loadHost(w, r)
	if !ok {
		return
	}
	err := s.d.Store.UnpinHostKey(r.Context(), e.Host.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "host not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not unpin the key")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
