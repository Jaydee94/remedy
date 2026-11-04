package signals

import "encoding/json"

// The bounds of what a monitoring signal may put into an incident, and the helpers that apply them. The Alertmanager source and the
// webhook source both use these, so there is one definition of "bounded alert text".
const (
	MaxLabels      = maxLabels
	MaxLabelKey    = maxLabelKey
	MaxLabelValue  = maxLabelValue
	MaxAnnotations = maxAnnotations
	MaxAnnotation  = maxAnnotation
	MaxTitle       = maxTitle
	MaxURL         = maxURL
)

// OneLine turns every run of white space and control characters into one space and trims the ends.
func OneLine(s string) string { return oneLine(s) }

// Cut cuts s to max characters and says whether it cut.
func Cut(s string, max int) (string, bool) { return cut(s, max) }

// CleanURL keeps a web address only, redacted and cut; anything else becomes empty.
func CleanURL(u string) string { return cleanURL(u) }

// SeverityOf maps a label to critical, warning, info or none.
func SeverityOf(label string) string { return severityOf(label) }

// Bounded keeps at most max entries, drops keys that are too long or that redaction would change, and cuts and redacts the values.
// It says whether it left anything out.
func Bounded(in map[string]string, max, maxValue int) (map[string]string, bool) {
	return bounded(in, max, maxValue)
}

// CompiledHosts are the alert rules of the hosts, parsed.
type CompiledHosts = []compiledHost

// CompileHosts parses the rules of every host. A host whose rules cannot be read is skipped and its name returned.
func CompileHosts(hosts []HostRules) (CompiledHosts, []string) { return compile(hosts) }

// MapHost returns the first host whose rules all hold for the labels (0 for none) and the names of the others that match too.
func MapHost(hosts CompiledHosts, labels map[string]string) (int64, []string) {
	return mapHost(hosts, labels)
}

// WithAlso writes the names of the further matching hosts into the details; none removes the key.
func WithAlso(details json.RawMessage, also []string) json.RawMessage { return withAlso(details, also) }
