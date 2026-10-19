// Command fakealertmanager serves a file as the answer of Alertmanager's GET /api/v2/alerts, for browser checks and the real run of
// Remedy against a machine without an Alertmanager. It is a dev tool: never run it in production.
//
//	fakealertmanager <addr> <token> <alerts.json>
//
// The file is read again at every request, so an edit is seen at the next poll. The query of the request (active, silenced,
// inhibited) is ignored: the file is the answer. While a file named <alerts.json>.down exists, the server answers 500. An empty
// <token> turns the bearer check off. The log has one line per request and never the value of the Authorization header.
package main

import (
	"crypto/subtle"
	"fmt"
	"log"
	"net/http"
	"os"
)

const alertsPath = "/api/v2/alerts"

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: fakealertmanager <addr> <token> <alerts.json>")
		os.Exit(2)
	}
	addr, token, file := os.Args[1], os.Args[2], os.Args[3]
	log.SetFlags(log.LstdFlags)
	log.Printf("serving %s at http://%s%s (token check: %t)", file, addr, alertsPath, token != "")
	srv := &http.Server{Addr: addr, Handler: handler(token, file)}
	log.Fatal(srv.ListenAndServe())
}

// handler answers the alerts of file. The token is compared in constant time.
func handler(token, file string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := serve(w, r, token, file)
		// The path only: the query can carry nothing secret here, but the log has no reason to hold it. Whether the header was right is
		// logged, its value never.
		authOK := token == "" || tokenOK(r.Header.Get("Authorization"), token)
		log.Printf("%s %s -> %d (authorization ok: %t)", r.Method, r.URL.Path, status, authOK)
	})
}

func serve(w http.ResponseWriter, r *http.Request, token, file string) int {
	if r.URL.Path != alertsPath {
		http.NotFound(w, r)
		return http.StatusNotFound
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return http.StatusMethodNotAllowed
	}
	if token != "" && !tokenOK(r.Header.Get("Authorization"), token) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return http.StatusUnauthorized
	}
	if _, err := os.Stat(file + ".down"); err == nil {
		http.Error(w, "down for the test", http.StatusInternalServerError)
		return http.StatusInternalServerError
	}
	body, err := os.ReadFile(file)
	if err != nil {
		http.Error(w, "cannot read the alerts file", http.StatusInternalServerError)
		return http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
	return http.StatusOK
}

func tokenOK(header, token string) bool {
	return subtle.ConstantTimeCompare([]byte(header), []byte("Bearer "+token)) == 1
}
