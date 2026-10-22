package sync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const raceOut = "fatal: Cannot fast-forward to multiple branches.\n"

// fakePulls replaces pullRunner for one test. Each call consumes the next
// scripted (output, error) pair; the last pair repeats. It returns a pointer to
// the call count so a test can assert how many attempts were made.
func fakePulls(t *testing.T, script ...struct {
	out string
	err error
}) *int {
	t.Helper()
	origRunner, origBackoff := pullRunner, pullRetryBackoff
	t.Cleanup(func() { pullRunner, pullRetryBackoff = origRunner, origBackoff })
	pullRetryBackoff = time.Millisecond
	calls := 0
	pullRunner = func(ctx context.Context, dir string) (string, error) {
		i := calls
		if i >= len(script) {
			i = len(script) - 1
		}
		calls++
		return script[i].out, script[i].err
	}
	return &calls
}

type pullStep = struct {
	out string
	err error
}

// cleanRepo makes a committed, clean repository so executePull gets past its
// dirty check and reaches the (faked) pull.
func cleanRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "git", "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# t"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "git", "add", ".")
	gitRun(t, dir, "git", "commit", "-q", "-m", "init")
	return dir
}

func TestExecutePull_RaceMessageThenSuccessIsNotAFailure(t *testing.T) {
	calls := fakePulls(t,
		pullStep{raceOut, errors.New("exit status 128")},
		pullStep{"Already up to date.\n", nil},
	)
	r := executePull(context.Background(), Task{RepoName: "r", Action: ActionPull, LocalDir: cleanRepo(t)})
	if r.Status != StatusUpToDate {
		t.Fatalf("status = %s (%q), want up to date: a one-off concurrent-fetch race must not fail the pass", r.Status, r.Summary)
	}
	if *calls != 2 {
		t.Errorf("pull attempts = %d, want 2", *calls)
	}
}

func TestExecutePull_PersistentRaceMessageStaysAFailure(t *testing.T) {
	calls := fakePulls(t, pullStep{raceOut, errors.New("exit status 128")})
	r := executePull(context.Background(), Task{RepoName: "r", Action: ActionPull, LocalDir: cleanRepo(t)})
	if r.Status != StatusFailed {
		t.Fatalf("status = %s, want failed: a persistent occurrence is a real misconfiguration and must stay visible", r.Status)
	}
	if want := 1 + pullRaceRetries; *calls != want {
		t.Errorf("pull attempts = %d, want %d (first try plus %d retries)", *calls, want, pullRaceRetries)
	}
}

func TestExecutePull_OtherFailuresAreNotRetried(t *testing.T) {
	for name, out := range map[string]string{
		"network":  "fatal: unable to access remote: Could not resolve host\n",
		"diverged": "hint: Not possible to fast-forward, aborting.\n",
	} {
		t.Run(name, func(t *testing.T) {
			calls := fakePulls(t, pullStep{out, errors.New("exit status 128")})
			executePull(context.Background(), Task{RepoName: "r", Action: ActionPull, LocalDir: cleanRepo(t)})
			if *calls != 1 {
				t.Errorf("pull attempts = %d, want 1: only the race message earns a retry", *calls)
			}
		})
	}
}

func TestPullWithRaceRetry_StopsWhenContextIsDone(t *testing.T) {
	calls := fakePulls(t, pullStep{raceOut, errors.New("exit status 128")})
	pullRetryBackoff = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := pullWithRaceRetry(ctx, t.TempDir())
	if err == nil || out != raceOut {
		t.Fatalf("got (%q, %v), want the race failure returned unchanged", out, err)
	}
	if *calls != 1 {
		t.Errorf("pull attempts = %d, want 1 once the context is done", *calls)
	}
}
