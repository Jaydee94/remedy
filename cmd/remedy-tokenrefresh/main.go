// Command remedy-tokenrefresh mints a short-lived token for one service account and stores it in one Secret. The Helm
// chart runs it as a CronJob for the control plane's write identity. See internal/tokenrefresh.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/Jaydee94/remedy/internal/kube"
	"github.com/Jaydee94/remedy/internal/tokenrefresh"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	var c tokenrefresh.Config
	flag.StringVar(&c.API, "api", envOr("REMEDY_K8S_API", kube.DefaultAPI), "the Kubernetes API server")
	flag.StringVar(&c.CAFile, "ca-file", os.Getenv("REMEDY_K8S_CA_FILE"), "its CA (default: the pod's own)")
	flag.StringVar(&c.TokenFile, "token-file", tokenrefresh.DefaultTokenFile, "the refresher's own service account token")
	flag.StringVar(&c.Namespace, "namespace", "", "the namespace of the account and the Secret (required)")
	flag.StringVar(&c.Account, "account", "remedy-write", "the service account to mint a token for")
	flag.StringVar(&c.Secret, "secret", "remedy-write-token", "the Secret to store the token in; it must exist")
	flag.StringVar(&c.Key, "key", "token", "the key in the Secret's data")
	flag.DurationVar(&c.Lifetime, "lifetime", 2*time.Hour, "how long the token is valid (10m to 24h)")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := tokenrefresh.Run(ctx, c, log); err != nil {
		log.Error("the token was not refreshed", "err", err)
		os.Exit(1)
	}
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
