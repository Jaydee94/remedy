package provider_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Jaydee94/remedy/internal/provider"
)

func TestLoginCommandArgsAndEnv(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin:/bin", "HOME=/home/x", "ANTHROPIC_API_KEY=sk-secret", "ANTHROPIC_AUTH_TOKEN=tok",
		"REMEDY_RUNNER_TOKEN=a-runner-token-of-24-chars-or-more",
	}
	cmd := provider.Claude{Binary: "/opt/claude"}.LoginCommand(context.Background(), parent)

	if cmd.Path != "/opt/claude" {
		t.Errorf("Path = %q", cmd.Path)
	}
	if got := cmd.Args[1:]; !slices.Equal(got, []string{"auth", "login", "--claudeai"}) {
		t.Errorf("args = %q", got)
	}
	for _, want := range []string{"DISABLE_UPDATES=1", "HOME=/home/x", "PATH=/usr/bin:/bin"} {
		if !slices.Contains(cmd.Env, want) {
			t.Errorf("env lacks %q: %q", want, cmd.Env)
		}
	}
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "REMEDY_RUNNER_TOKEN"} {
		for _, kv := range cmd.Env {
			if strings.HasPrefix(kv, key+"=") {
				t.Errorf("env carries %s", key)
			}
		}
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Errorf("SysProcAttr = %+v, want Setpgid", cmd.SysProcAttr)
	}
	if cmd.Dir != os.TempDir() {
		t.Errorf("Dir = %q, want the temp dir", cmd.Dir)
	}
	if cmd.WaitDelay != 3*time.Second {
		t.Errorf("WaitDelay = %s", cmd.WaitDelay)
	}
	if cmd.Cancel == nil {
		t.Error("Cancel is not set")
	}
	if cmd.Stdin != nil || cmd.Stdout != nil || cmd.Stderr != nil {
		t.Error("the caller owns stdin, stdout and stderr")
	}
}

func TestLoginCommandDefaultsToClaudeOnThePath(t *testing.T) {
	cmd := provider.Claude{}.LoginCommand(context.Background(), nil)
	if cmd.Args[0] != "claude" {
		t.Errorf("Args[0] = %q", cmd.Args[0])
	}
}

func TestFilterEnvStillDropsDisableUpdates(t *testing.T) {
	if got := provider.FilterEnv([]string{"DISABLE_UPDATES=1"}); len(got) != 0 {
		t.Fatalf("FilterEnv kept %q", got)
	}
}

// groupScript backgrounds a sleeper, records its pid and waits.
func groupScript(t *testing.T) (script, pidFile string) {
	t.Helper()
	dir := t.TempDir()
	pidFile = filepath.Join(dir, "grandchild.pid")
	script = filepath.Join(dir, "claude")
	body := "#!/bin/sh\nsleep 30 &\necho $! > '" + pidFile + "'\nwait\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script, pidFile
}

func waitForPid(t *testing.T, file string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(file); err == nil && strings.HasSuffix(string(b), "\n") {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the grandchild never wrote its pid")
	return 0
}

func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("process %d is still alive", pid)
}

func TestSignalGroupEndsAGrandchild(t *testing.T) {
	script, pidFile := groupScript(t)
	cmd := provider.Claude{Binary: script}.LoginCommand(context.Background(), []string{"PATH=/usr/bin:/bin"})
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	grandchild := waitForPid(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(grandchild, syscall.SIGKILL) })

	if err := provider.SignalGroup(cmd, syscall.SIGTERM); err != nil {
		t.Fatalf("SignalGroup: %v", err)
	}
	_ = cmd.Wait()
	waitGone(t, cmd.Process.Pid)
	waitGone(t, grandchild)
}

func TestSignalGroupWithoutAProcessIsANoOp(t *testing.T) {
	cmd := provider.Claude{Binary: "/nonexistent"}.LoginCommand(context.Background(), nil)
	if err := provider.SignalGroup(cmd, syscall.SIGTERM); err != nil {
		t.Fatalf("SignalGroup before Start: %v", err)
	}
}

func TestCancellingTheContextEndsTheGroup(t *testing.T) {
	script, pidFile := groupScript(t)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := provider.Claude{Binary: script}.LoginCommand(ctx, []string{"PATH=/usr/bin:/bin"})
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	grandchild := waitForPid(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(grandchild, syscall.SIGKILL) })

	cancel()
	_ = cmd.Wait()
	waitGone(t, grandchild)
}
