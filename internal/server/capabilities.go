package server

import "net/http"

// Cluster says what the control plane can do in a cluster, for the UI and for creating runs. The zero value is no
// cluster. It carries no address and no token.
type Cluster struct {
	Read       bool     // the cluster can be read: runs can have cluster tools
	Write      bool     // cluster actions are possible, after an approval
	Namespaces []string // where actions may be used
	// NamespacesFunc, when set, is asked at every request instead of Namespaces.
	NamespacesFunc func() []string
}

type clusterView struct {
	Read       bool     `json:"read"`
	Write      bool     `json:"write"`
	Namespaces []string `json:"namespaces"`
}

// capabilities tells the UI what it can offer. Nothing in it is secret, but it is behind the session like the rest.
func (s *srv) capabilities(w http.ResponseWriter, _ *http.Request) {
	ns := s.d.Cluster.Namespaces
	if s.d.Cluster.NamespacesFunc != nil {
		ns = s.d.Cluster.NamespacesFunc()
	}
	v := clusterView{Read: s.d.Cluster.Read, Write: s.d.Cluster.Write, Namespaces: ns}
	if v.Namespaces == nil {
		v.Namespaces = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"cluster": v})
}
