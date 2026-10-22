// Command fakemetrics is a fake metrics store (the Prometheus HTTP API) for browser checks, on a machine that has none. It answers the
// build info, an empty instant query and an empty range query. Never use it in production.
package main

import (
	"crypto/subtle"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	buildInfo   = `{"status":"success","data":{"version":"2.53.0","revision":"fake","branch":"main"}}`
	emptyVector = `{"status":"success","data":{"resultType":"vector","result":[]}}`
	emptyMatrix = `{"status":"success","data":{"resultType":"matrix","result":[]}}`
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: fakemetrics <addr> <token> (an empty token turns the check off; touch <tmp>/fakemetrics.down to make it fail)")
		os.Exit(2)
	}
	addr, token := os.Args[1], os.Args[2]
	down := os.TempDir() + "/fakemetrics.down"
	log.Printf("serving http://%s/api/v1/... (token check: %t, down file: %s)", addr, token != "", down)
	srv := &http.Server{Addr: addr, Handler: handler(token, down), ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

// handler answers like a metrics store under any base path. The log shows whether the Authorization header was right, never its value.
func handler(token, downFile string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := serve(w, r, token, downFile)
		authOK := token == "" || tokenOK(r.Header.Get("Authorization"), token)
		log.Printf("%s %s -> %d (authorization ok: %t)", r.Method, r.URL.Path, status, authOK)
	})
}

func serve(w http.ResponseWriter, r *http.Request, token, downFile string) int {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return http.StatusMethodNotAllowed
	}
	if token != "" && !tokenOK(r.Header.Get("Authorization"), token) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return http.StatusUnauthorized
	}
	if _, err := os.Stat(downFile); err == nil {
		http.Error(w, "down for the test", http.StatusInternalServerError)
		return http.StatusInternalServerError
	}
	var body string
	switch {
	case strings.HasSuffix(r.URL.Path, "/api/v1/status/buildinfo"):
		body = buildInfo
	case strings.HasSuffix(r.URL.Path, "/api/v1/query_range"):
		body = emptyMatrix
	case strings.HasSuffix(r.URL.Path, "/api/v1/query"):
		body = emptyVector
	default:
		http.NotFound(w, r)
		return http.StatusNotFound
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
	return http.StatusOK
}

func tokenOK(header, token string) bool {
	return subtle.ConstantTimeCompare([]byte(header), []byte("Bearer "+token)) == 1
}
