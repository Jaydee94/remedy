package app

import (
	"errors"
	"log/slog"

	"github.com/Jaydee94/remedy/internal/alertmanager"
	"github.com/Jaydee94/remedy/internal/config"
	"github.com/Jaydee94/remedy/internal/gatekeeper"
	"github.com/Jaydee94/remedy/internal/metrics"
	"github.com/Jaydee94/remedy/internal/secret"
	"github.com/Jaydee94/remedy/internal/settings"
)

// signalTools builds the read tools of the monitoring. They are always registered and offered only while their address is set, because
// the addresses are settings that change without a restart. A client that cannot be set up (a CA bundle without a certificate) leaves
// its tools out for good: the reader stays a nil interface, never a nil pointer inside one.
func signalTools(cfg config.Server, set *settings.Settings, log *slog.Logger) []gatekeeper.Tool {
	var am gatekeeper.AlertReader
	if c, err := alertmanager.New(settingCreds(set, settings.AlertmanagerURL, settings.AlertmanagerToken), cfg.AlertmanagerCAFile, alertmanager.WithLog(log)); err != nil {
		log.Error("the Alertmanager tools are off: cannot set up their client", "err", err)
	} else {
		am = c
	}
	var mx gatekeeper.MetricsReader
	if c, err := metrics.New(settingCreds(set, settings.MetricsURL, settings.MetricsToken), cfg.MetricsCAFile, metrics.WithLog(log)); err != nil {
		log.Error("the metrics tools are off: cannot set up their client", "err", err)
	} else {
		mx = c
	}
	return gatekeeper.SignalTools(am, mx,
		func() bool { return set.Text(settings.AlertmanagerURL) != "" },
		func() bool { return set.Text(settings.MetricsURL) != "" },
		nil)
}

// settingCreds reads an address and its sealed token from the settings at every request. A token that is not set means none is sent.
func settingCreds(set *settings.Settings, urlKey, tokenKey string) func() (string, secret.Value, error) {
	return func() (string, secret.Value, error) {
		base := set.Text(urlKey)
		tok, err := set.Secret(tokenKey)
		switch {
		case errors.Is(err, settings.ErrNotSet):
			return base, secret.Value{}, nil
		case err != nil:
			return "", secret.Value{}, err
		}
		return base, tok, nil
	}
}
