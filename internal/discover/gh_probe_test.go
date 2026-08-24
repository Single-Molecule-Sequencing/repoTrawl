package discover

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The defect these guard, measured 2026-08-24 on the RDLU0053 hub:
// GhAvailable probed with `gh auth status`, which enumerates every user in
// hosts.yml and consults the system secret service for each one. That host's
// gnome-keyring collection was LOCKED, so the command blocked forever on an
// unlock prompt nothing was present to answer. ghAuthTimeout then fired, the
// probe reported "not available or not authenticated", and repoTrawl exited 2
// on a host where `gh api user` answered in 0.27s using the same token from
// the same hosts.yml.
//
// So "the probe passes" is not the property worth testing. Two are: the probe
// must not be a command that can wedge, and its failure must say WHICH of the
// three failure modes happened, because collapsing them is what sent a reader
// to run `gh auth login` on a host whose authentication was fine.

// fakeGh installs a stub `gh` at the front of PATH and returns the path of the
// file that stub appends its argv to.
func fakeGh(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	argsLog := filepath.Join(dir, "argv.log")
	body := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + argsLog + "\n" + script + "\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argsLog
}

// TestGhProbeNeverRunsAuthStatus is the direct regression guard. `gh auth
// status` is the one gh subcommand that walks the secret service per user, so
// naming it by string here is the point, not an implementation detail.
func TestGhProbeNeverRunsAuthStatus(t *testing.T) {
	argsLog := fakeGh(t, "exit 0")

	if err := GhProbe(context.Background()); err != nil {
		t.Fatalf("probe should succeed against a gh that exits 0, got: %v", err)
	}

	raw, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatalf("stub gh was never invoked: %v", err)
	}
	got := strings.TrimSpace(string(raw))
	if got == "" {
		t.Fatal("stub gh was invoked with no arguments")
	}
	if strings.Contains(got, "auth status") {
		t.Fatalf("probe ran `gh auth status`, the exact command that wedges on a locked keyring; argv was %q", got)
	}
}

// TestGhProbeReportsATimeoutDistinctly covers the hang itself.
func TestGhProbeReportsATimeoutDistinctly(t *testing.T) {
	fakeGh(t, "sleep 30")

	restore := ghAuthTimeout
	ghAuthTimeout = 250 * time.Millisecond
	t.Cleanup(func() { ghAuthTimeout = restore })

	start := time.Now()
	err := GhProbe(context.Background())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a gh that never returns must fail the probe")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("probe did not honor its timeout; took %s", elapsed)
	}
	if !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("a wedged gh must be reported as a timeout, got: %v", err)
	}
	if strings.Contains(err.Error(), "not authenticated") {
		t.Fatalf("a wedged gh must NOT be reported as an auth failure, got: %v", err)
	}
	if GhAvailable(context.Background()) {
		t.Fatal("GhAvailable must be false when the probe times out")
	}
}

// TestGhProbeReportsAMissingBinaryDistinctly keeps the third failure mode
// separable from the other two.
func TestGhProbeReportsAMissingBinaryDistinctly(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	err := GhProbe(context.Background())
	if err == nil {
		t.Fatal("probe must fail when gh is not on PATH")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("a missing gh must say so, got: %v", err)
	}
	if strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("a missing gh must not be reported as a timeout, got: %v", err)
	}
}

// TestGhProbeSurfacesGhsOwnMessage covers a genuinely signed-out gh: the
// operator needs gh's own words, not ours.
func TestGhProbeSurfacesGhsOwnMessage(t *testing.T) {
	fakeGh(t, "echo 'gh: To get started with GitHub CLI, please run: gh auth login' >&2; exit 4")

	err := GhProbe(context.Background())
	if err == nil {
		t.Fatal("probe must fail when gh exits non-zero")
	}
	if !strings.Contains(err.Error(), "gh auth login") {
		t.Fatalf("probe must surface gh's own stderr, got: %v", err)
	}
}
