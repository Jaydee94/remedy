// Package version says which build is running. The release sets Version and Commit at link time:
//
//	go build -ldflags "-X github.com/Jaydee94/remedy/internal/version.Version=0.9.0 -X github.com/Jaydee94/remedy/internal/version.Commit=<sha>"
//
// A plain go build leaves "dev", and the commit is then read from the build information of a checkout, when there is one.
package version

import (
	"runtime/debug"
	"strings"
)

// Version and Commit are set with -ldflags -X. Read them through Get, which checks them.
var (
	Version = "dev"
	Commit  = ""
)

// shortCommit is how many characters of a commit are kept.
const shortCommit = 7

// maxLen bounds a version or commit string.
const maxLen = 64

// Info is what the UI and the API show.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// Get is the running build. A value that is not a short string of [0-9A-Za-z._+-] is replaced by "unknown": it ends up in the UI and in an API.
func Get() Info { return get(Version, Commit, debug.ReadBuildInfo) }

func get(version, commit string, read func() (*debug.BuildInfo, bool)) Info {
	if version == "" {
		version = "dev"
	}
	if commit == "" {
		if bi, ok := read(); ok && bi != nil {
			for _, s := range bi.Settings {
				if s.Key == "vcs.revision" {
					commit = s.Value
				}
			}
		}
	}
	return Info{Version: CleanVersion(version), Commit: CleanCommit(commit)}
}

// String is "0.9.0 (abc1234)", "edge (abc1234)" or "dev" when there is no commit.
func (i Info) String() string {
	if i.Commit == "" {
		return i.Version
	}
	return i.Version + " (" + i.Commit + ")"
}

// CleanVersion returns s when it is empty or a short string of [0-9A-Za-z._+-], and "unknown" otherwise. The control plane
// applies it to what a runner says about itself.
func CleanVersion(s string) string {
	if s == "" {
		return ""
	}
	if len(s) > maxLen || strings.IndexFunc(s, func(r rune) bool { return !safe(r) }) >= 0 {
		return "unknown"
	}
	return s
}

// CleanCommit is CleanVersion followed by cutting the commit to its first 7 characters.
func CleanCommit(s string) string {
	s = CleanVersion(s)
	if len(s) > shortCommit && s != "unknown" {
		return s[:shortCommit]
	}
	return s
}

func safe(r rune) bool {
	return r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '.' || r == '_' || r == '+' || r == '-'
}
