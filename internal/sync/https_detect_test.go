package sync

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// detectHTTPSAuth runs from a package-level initializer, so it executes before
// main(), before flag parsing, and before `-V` can print a version and return.
// Anything unbounded here is unbounded for the entire program.
//
// Measured 2026-08-24 on the RDLU0053 hub: the old body was
// `exec.Command("gh","auth","status")` with no context and no timeout, and
// `repoupdater -V` never returned -- the binary wedged in this initializer
// against a locked system keyring. These guard the two properties that stops:
// the probe must be bounded, and it must not be `gh auth status`.

func stubGh(t *testing.T, script string) string {
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

// TestDetectHTTPSAuthIsBounded is the regression guard for the wedge. A gh that
// never returns must not be able to hold the process open.
func TestDetectHTTPSAuthIsBounded(t *testing.T) {
	stubGh(t, "sleep 60")

	done := make(chan bool, 1)
	start := time.Now()
	go func() { done <- detectHTTPSAuth() }()

	select {
	case got := <-done:
		if elapsed := time.Since(start); elapsed > httpsDetectTimeout+3*time.Second {
			t.Fatalf("detectHTTPSAuth took %s, past its %s bound", elapsed, httpsDetectTimeout)
		}
		if got {
			t.Fatal("a gh that never answers must not be read as 'HTTPS auth confirmed'")
		}
	case <-time.After(httpsDetectTimeout + 10*time.Second):
		t.Fatalf("detectHTTPSAuth never returned; this is the exact wedge that made `repoupdater -V` hang forever")
	}
}

// TestDetectHTTPSAuthNeverRunsAuthStatus pins the command itself. `gh auth
// status` is the one subcommand that walks the secret service per non-active
// user, so naming it here is the point.
func TestDetectHTTPSAuthNeverRunsAuthStatus(t *testing.T) {
	argsLog := stubGh(t, "echo https; exit 0")

	if !detectHTTPSAuth() {
		t.Fatal("a gh reporting https must be detected as HTTPS auth")
	}

	raw, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatalf("stub gh was never invoked: %v", err)
	}
	got := strings.TrimSpace(string(raw))
	if strings.Contains(got, "auth status") {
		t.Fatalf("startup probe ran `gh auth status`, which can wedge on a locked keyring; argv was %q", got)
	}
}

// TestDetectHTTPSAuthReadsSSH keeps the negative case honest: this function
// gates a git URL rewrite, so a wrong true is not harmless.
func TestDetectHTTPSAuthReadsSSH(t *testing.T) {
	stubGh(t, "echo ssh; exit 0")

	if detectHTTPSAuth() {
		t.Fatal("a gh reporting ssh must not be read as HTTPS auth")
	}
}

// TestDetectHTTPSAuthHandlesMissingGh covers the documented fallback.
func TestDetectHTTPSAuthHandlesMissingGh(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if detectHTTPSAuth() {
		t.Fatal("with no gh on PATH the probe must fall back to false, not claim HTTPS")
	}
	// Keep the linter honest about the unused import in some build configs.
	_ = exec.ErrNotFound
}
