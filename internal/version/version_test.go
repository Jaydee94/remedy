package version

import (
	"runtime/debug"
	"strings"
	"testing"
)

func buildInfo(rev string) func() (*debug.BuildInfo, bool) {
	return func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: rev}}}, true
	}
}

func noBuildInfo() (*debug.BuildInfo, bool) { return nil, false }

func TestGetAndString(t *testing.T) {
	cases := []struct {
		name            string
		version, commit string
		bi              func() (*debug.BuildInfo, bool)
		want            Info
		str             string
	}{
		{"a release", "0.9.0", "abc1234def5678", noBuildInfo, Info{"0.9.0", "abc1234"}, "0.9.0 (abc1234)"},
		{"an edge build", "edge", "abc1234def5678", noBuildInfo, Info{"edge", "abc1234"}, "edge (abc1234)"},
		{"a short commit stays", "0.9.0", "abc12", noBuildInfo, Info{"0.9.0", "abc12"}, "0.9.0 (abc12)"},
		{"no ldflags, no build info", "dev", "", noBuildInfo, Info{"dev", ""}, "dev"},
		{"no commit falls back to the VCS revision", "dev", "", buildInfo("1234567890abcdef"), Info{"dev", "1234567"}, "dev (1234567)"},
		{"the ldflags commit wins over the VCS revision", "dev", "abcdef0123", buildInfo("1234567890abcdef"), Info{"dev", "abcdef0"}, "dev (abcdef0)"},
		{"build info without a revision", "dev", "", func() (*debug.BuildInfo, bool) { return &debug.BuildInfo{}, true }, Info{"dev", ""}, "dev"},
		{"an empty version is dev", "", "", noBuildInfo, Info{"dev", ""}, "dev"},
		{"a hostile version", "1.0<script>", "abc1234", noBuildInfo, Info{"unknown", "abc1234"}, "unknown (abc1234)"},
		{"a hostile commit", "1.0.0", "abc 123;rm", noBuildInfo, Info{"1.0.0", "unknown"}, "1.0.0 (unknown)"},
		{"a hostile VCS revision", "dev", "", buildInfo("x\ny"), Info{"dev", "unknown"}, "dev (unknown)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := get(c.version, c.commit, c.bi)
			if got != c.want {
				t.Fatalf("get = %+v, want %+v", got, c.want)
			}
			if got.String() != c.str {
				t.Fatalf("String = %q, want %q", got.String(), c.str)
			}
		})
	}
}

func TestGetReadsThePackageVariables(t *testing.T) {
	oldV, oldC := Version, Commit
	t.Cleanup(func() { Version, Commit = oldV, oldC })
	Version, Commit = "9.9.9", "0123456789abcdef"
	if got := Get(); got != (Info{"9.9.9", "0123456"}) {
		t.Fatalf("Get = %+v", got)
	}
}

func TestCleanKeepsOnlyASafeAlphabet(t *testing.T) {
	for _, ok := range []string{"", "0.9.0", "edge", "v0.9.0-3-gabc1234-dirty", "1.2.3+build.5", "a_b"} {
		if got := CleanVersion(ok); got != ok {
			t.Errorf("CleanVersion(%q) = %q, want it unchanged", ok, got)
		}
	}
	for _, bad := range []string{"1 2", "1.0\n", "<b>", "ä", "1;2", "a/b", `a"b`, strings.Repeat("1", 65)} {
		if got := CleanVersion(bad); got != "unknown" {
			t.Errorf("CleanVersion(%q) = %q, want unknown", bad, got)
		}
	}
	if got := CleanCommit("0123456789abcdef"); got != "0123456" {
		t.Errorf("CleanCommit long = %q", got)
	}
	if got := CleanCommit("abc;"); got != "unknown" {
		t.Errorf("CleanCommit hostile = %q", got)
	}
	if got := CleanCommit(""); got != "" {
		t.Errorf("CleanCommit empty = %q", got)
	}
}
