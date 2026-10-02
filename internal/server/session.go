package server

import (
	"encoding/json"
	"net/http"

	"github.com/Jaydee94/remedy/internal/auth"
)

const (
	cookieName = "remedy_session"
	csrfHeader = "X-Remedy-CSRF"
)

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func needsCSRF(r *http.Request) bool {
	return r.Method != http.MethodGet && r.Method != http.MethodHead
}

// session guards an admin handler: valid session cookie, plus the CSRF header on state changes.
func (s *srv) session(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil || !s.d.Auth.ValidSession(c.Value) {
			writeErr(w, http.StatusUnauthorized, "not logged in")
			return
		}
		if needsCSRF(r) && r.Header.Get(csrfHeader) != "1" {
			writeErr(w, http.StatusForbidden, "missing "+csrfHeader+" header")
			return
		}
		next(w, r)
	}
}

func (s *srv) login(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(csrfHeader) != "1" {
		writeErr(w, http.StatusForbidden, "missing "+csrfHeader+" header")
		return
	}
	ip := clientIP(r)
	if s.d.Auth.Locked(ip) {
		writeErr(w, http.StatusTooManyRequests, "too many failed attempts, try again later")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !s.d.Auth.Verify(req.Password) {
		s.d.Auth.RecordFailure(ip)
		writeErr(w, http.StatusUnauthorized, "wrong password")
		return
	}
	s.d.Auth.ResetFailures(ip)
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    s.d.Auth.NewSession(),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   isHTTPS(r),
		MaxAge:   int(auth.SessionTTL.Seconds()),
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.d.Auth.EndSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) me(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"user": "admin"})
}
