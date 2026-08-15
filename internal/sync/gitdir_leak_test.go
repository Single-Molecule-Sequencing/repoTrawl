package sync

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Guard: an inherited GIT_DIR must never redirect this suite at a real repo.
//
// git exports GIT_DIR into every hook process. repoTrawl's registered gate,
// ci-repotrawl, runs `go test ./... -count=1`, and the lab's pre-push hook runs
// that gate. GIT_DIR outranks cmd.Dir exactly as it outranks `git -C`, and when
// GIT_DIR is set while GIT_WORK_TREE is not, git treats the CURRENT directory
// as the work tree. So a test that looks perfectly hermetic reads its files
// from t.TempDir() and writes its objects and refs into whatever clone the
// pusher happened to be in.
//
// sync_test.go is the dangerous one in this package: it runs `git init`,
// `git commit`, `git add .`, a bare `git push` (which pushes the current branch
// to its configured upstream, i.e. the real GitHub remote), and
// `git --git-dir <bare> update-ref -d refs/heads/main`.
//
// This is not hypothetical. On 2026-08-15 the same mechanism destroyed
// lab-system's main: it was replaced by a commit authored by
// Test <test@example.com> whose entire tree was one file, f.txt. Write-up:
// lab-system/docs/failures/2026-08-15-gitdir-leak-into-pytest.md
//
// Go has no conftest.py, so unlike the Python repos this scrub in the test
// helper is the only in-repo layer available.

// scrubbedEnviron and redirectingGitVars now live in sync.go, because the
// production code needs them too: gitEnv() feeds every git command this
// package runs.

// mustGit runs git for TEST SETUP with an explicitly clean environment, so the
// fixtures this guard depends on cannot themselves be redirected.
func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(scrubbedEnviron(),
		"GIT_AUTHOR_NAME=guard",
		"GIT_AUTHOR_EMAIL=guard@example.invalid",
		"GIT_COMMITTER_NAME=guard",
		"GIT_COMMITTER_EMAIL=guard@example.invalid",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("setup git %v failed: %s\n%s", args, err, out)
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = scrubbedEnviron()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %s\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestGitEnvDropsInheritedGitDir covers the PRODUCTION path. gitEnv() feeds
// cmd.Env at sync.go:70, :85, :96 and :108, every one of them paired with a
// cmd.Dir. repoTrawl's whole job is running git across many clones, so an
// inherited GIT_DIR would silently aim all of them at one repo.
func TestGitEnvDropsInheritedGitDir(t *testing.T) {
	t.Setenv("GIT_DIR", "/somewhere/.git")
	t.Setenv("GIT_WORK_TREE", "/somewhere")

	for _, kv := range gitEnv() {
		if strings.HasPrefix(kv, "GIT_DIR=") || strings.HasPrefix(kv, "GIT_WORK_TREE=") {
			t.Fatalf("gitEnv() passed a repo-redirection variable to git: %s", kv)
		}
	}
}

// TestGitEnvKeepsItsOwnSettings guards against over-scrubbing: the prompt
// suppression gitEnv exists to set must survive.
func TestGitEnvKeepsItsOwnSettings(t *testing.T) {
	t.Setenv("GIT_DIR", "/somewhere/.git")

	var found bool
	for _, kv := range gitEnv() {
		if kv == "GIT_TERMINAL_PROMPT=0" {
			found = true
		}
	}
	if !found {
		t.Fatal("gitEnv() lost GIT_TERMINAL_PROMPT=0")
	}
}

// TestScrubbedEnvironDropsRedirectionVariables pins the helper itself.
func TestScrubbedEnvironDropsRedirectionVariables(t *testing.T) {
	for _, name := range []string{
		"GIT_DIR",
		"GIT_WORK_TREE",
		"GIT_INDEX_FILE",
		"GIT_OBJECT_DIRECTORY",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES",
		"GIT_COMMON_DIR",
		"GIT_NAMESPACE",
	} {
		t.Setenv(name, "/somewhere/.git")
	}
	t.Setenv("REPOTRAWL_GUARD_MARKER", "kept")

	got := scrubbedEnviron()

	for _, kv := range got {
		if strings.HasPrefix(kv, "GIT_DIR=") ||
			strings.HasPrefix(kv, "GIT_WORK_TREE=") ||
			strings.HasPrefix(kv, "GIT_INDEX_FILE=") ||
			strings.HasPrefix(kv, "GIT_OBJECT_DIRECTORY=") ||
			strings.HasPrefix(kv, "GIT_ALTERNATE_OBJECT_DIRECTORIES=") ||
			strings.HasPrefix(kv, "GIT_COMMON_DIR=") ||
			strings.HasPrefix(kv, "GIT_NAMESPACE=") {
			t.Fatalf("redirection variable survived the scrub: %s", kv)
		}
	}
	var kept bool
	for _, kv := range got {
		if kv == "REPOTRAWL_GUARD_MARKER=kept" {
			kept = true
		}
	}
	if !kept {
		t.Fatal("scrubbedEnviron dropped an unrelated variable; only git redirection should go")
	}
}

// TestGitRunCannotWriteIntoAnInheritedGitDir reproduces the 2026-08-15
// incident's exact signature and asserts it can no longer happen: without the
// scrub, the victim gains a "commit1" whose whole tree is f.txt, on top of its
// real history.
func TestGitRunCannotWriteIntoAnInheritedGitDir(t *testing.T) {
	victim := t.TempDir()
	mustGit(t, victim, "init", "-q")
	mustGit(t, victim, "config", "user.email", "guard@example.invalid")
	mustGit(t, victim, "config", "user.name", "guard")
	if err := os.WriteFile(filepath.Join(victim, "real.txt"), []byte("real\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, victim, "add", "-A")
	mustGit(t, victim, "commit", "-qm", "real history")

	before := gitOut(t, victim, "rev-list", "--count", "HEAD")
	if before != "1" {
		t.Fatalf("fixture: expected 1 commit, got %s", before)
	}

	// The leak, exactly as a pre-push hook delivers it.
	t.Setenv("GIT_DIR", filepath.Join(victim, ".git"))

	sandbox := t.TempDir()
	gitRun(t, sandbox, "git", "init")
	gitRun(t, sandbox, "git", "config", "user.email", "t@t.t")
	gitRun(t, sandbox, "git", "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(sandbox, "f.txt"), []byte("junk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, sandbox, "git", "add", "-A")
	gitRun(t, sandbox, "git", "commit", "-qm", "commit1")

	if after := gitOut(t, victim, "rev-list", "--count", "HEAD"); after != before {
		t.Fatalf("the test suite wrote into the real repo through GIT_DIR: %s -> %s commits",
			before, after)
	}
	if tree := gitOut(t, victim, "ls-tree", "-r", "HEAD", "--name-only"); tree != "real.txt" {
		t.Fatalf("real repo tree was replaced: %q", tree)
	}
}
