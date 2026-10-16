package signals

import (
	"encoding/json"
	"sort"

	"github.com/Jaydee94/remedy/internal/alertmatch"
)

// HostRules are the alert rules of one host as they are stored.
type HostRules struct {
	ID       int64
	Name     string
	Matchers string
}

type compiledHost struct {
	id    int64
	name  string
	rules []alertmatch.Matcher
}

// compile parses the rules of every host, in the order of their ids. A host whose rules cannot be read is skipped and its name is
// returned (the server checks the rules, so this means a row that was changed by hand).
func compile(hosts []HostRules) (ok []compiledHost, skipped []string) {
	sorted := append([]HostRules(nil), hosts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for _, h := range sorted {
		rules, err := alertmatch.Parse(h.Matchers)
		if err != nil {
			skipped = append(skipped, h.Name)
			continue
		}
		if len(rules) > 0 {
			ok = append(ok, compiledHost{id: h.ID, name: h.Name, rules: rules})
		}
	}
	return ok, skipped
}

// mapHost returns the first host whose rules all hold for the labels (0 for none) and the names of the others that match too.
func mapHost(hosts []compiledHost, labels map[string]string) (int64, []string) {
	var first int64
	var also []string
	for _, h := range hosts {
		if !alertmatch.Matches(h.rules, labels) {
			continue
		}
		if first == 0 {
			first = h.id
			continue
		}
		also = append(also, h.name)
	}
	return first, also
}

// withAlso writes the names of the further matching hosts into the details; none removes the key.
func withAlso(details json.RawMessage, also []string) json.RawMessage {
	var m map[string]any
	if err := json.Unmarshal(details, &m); err != nil || m == nil {
		m = map[string]any{}
	}
	if len(also) > 0 {
		m["alsoMatches"] = also
	} else {
		delete(m, "alsoMatches")
	}
	b, err := json.Marshal(m)
	if err != nil {
		return details
	}
	return b
}
