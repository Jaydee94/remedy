// Package sshhost is Remedy's SSH access to hosts: the checks on a host's fields, the onboarding snippet, a client that only
// talks to a pinned host key, and the report of what an account may do.
package sshhost

import (
	"net"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	MaxNotes = 2000
	MaxUnits = 20
)

var (
	nameRE     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$`)
	hostnameRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	accountRE  = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	dockerRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}\*?$`)
	unitRE     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@:-]{0,100}\.(service|timer|socket)$`)
)

// FieldError is a problem with one field, worded so that it can be shown to the maintainer as it is.
type FieldError struct{ Field, Msg string }

func (e *FieldError) Error() string { return e.Msg }

// Fields are the editable fields of a host that end up in a file or a connection.
type Fields struct {
	Name          string
	Address       string
	Port          int
	Account       string
	Notes         string
	DockerPattern string
	SystemdUnits  []string
}

// Validate checks every field and normalises SystemdUnits in place. The first problem is returned.
func (f *Fields) Validate() error {
	if !nameRE.MatchString(f.Name) {
		return &FieldError{"name", "the name may contain letters, digits, dot, dash and underscore, 1 to 40 characters"}
	}
	if net.ParseIP(f.Address) == nil && !hostnameRE.MatchString(f.Address) {
		return &FieldError{"address", "the address must be a host name or an IP address"}
	}
	if f.Port < 1 || f.Port > 65535 {
		return &FieldError{"port", "the port must be between 1 and 65535"}
	}
	if err := checkAccount(f.Account); err != nil {
		return err
	}
	if utf8.RuneCountInString(f.Notes) > MaxNotes || strings.ContainsRune(f.Notes, 0) {
		return &FieldError{"notes", "the notes may have at most 2000 characters"}
	}
	if err := checkDocker(f.DockerPattern); err != nil {
		return err
	}
	units, err := normalizeUnits(f.SystemdUnits)
	if err != nil {
		return err
	}
	f.SystemdUnits = units
	return nil
}

func checkAccount(a string) error {
	if !accountRE.MatchString(a) {
		return &FieldError{"account", "the account must be a lower-case user name such as remedy"}
	}
	return nil
}

// checkDocker accepts an empty pattern (no docker rules) or one container name that may end in a single star.
func checkDocker(p string) error {
	if p == "" {
		return nil
	}
	if !dockerRE.MatchString(p) || strings.EqualFold(p, "ALL") {
		return &FieldError{"dockerPattern", "the container pattern must be a name such as remedy-*, with at most one star at the end"}
	}
	return nil
}

// normalizeUnits gives a unit without .service, .timer or .socket the suffix .service, drops duplicates and checks each unit.
func normalizeUnits(units []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, u := range units {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		if !strings.HasSuffix(u, ".service") && !strings.HasSuffix(u, ".timer") && !strings.HasSuffix(u, ".socket") {
			u += ".service"
		}
		if !unitRE.MatchString(u) {
			return nil, &FieldError{"systemdUnits", "a systemd unit may contain letters, digits and _ . @ : -, and ends in .service, .timer or .socket"}
		}
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	if len(out) > MaxUnits {
		return nil, &FieldError{"systemdUnits", "at most 20 units"}
	}
	return out, nil
}
