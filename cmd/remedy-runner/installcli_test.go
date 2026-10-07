package main

import (
	"strings"
	"testing"
)

func TestPlatformFlagParsesGoarchNameAndChecksum(t *testing.T) {
	var p platformFlag
	sha := strings.Repeat("a", 64)
	if err := p.Set("amd64=linux-x64@" + sha); err != nil {
		t.Fatal(err)
	}
	if err := p.Set("arm64=linux-arm64@" + sha); err != nil {
		t.Fatal(err)
	}
	got := p.platforms()
	if got["amd64"].Name != "linux-x64" || got["arm64"].Name != "linux-arm64" || got["arm64"].SHA256 != sha {
		t.Fatalf("platforms = %+v", got)
	}
}

func TestPlatformFlagRefusesWhatIsNotThreeParts(t *testing.T) {
	var p platformFlag
	for _, bad := range []string{"", "amd64", "amd64=linux-x64", "=linux-x64@abc", "amd64=@abc", "amd64=linux-x64@", "amd64=a=b@c"} {
		if err := p.Set(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
	var dup platformFlag
	if err := dup.Set("amd64=a@b"); err != nil {
		t.Fatal(err)
	}
	if err := dup.Set("amd64=c@d"); err == nil {
		t.Error("a second value for the same architecture must be refused")
	}
}

func TestInstallCLIFailsWithoutItsRequiredFlags(t *testing.T) {
	if code := installCLI(nil); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if code := installCLI([]string{"--version", "1.0.0"}); code != 2 {
		t.Fatalf("exit code without a template and a platform = %d, want 2", code)
	}
}
