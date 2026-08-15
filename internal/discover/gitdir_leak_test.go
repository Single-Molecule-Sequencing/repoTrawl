package discover

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Same guard as internal/sync/gitdir_leak_test.go, for this package's own
// gitRun helper. That one set no cmd.Env at all, so it inherited the whole
// process environment including any GIT_DIR, which outranks cmd.Dir.
//
// See lab-system/docs/failures/2026-08-15-gitdir-leak-into-pytest.md

// scrubbedEnviron and redirectingGitVars now live in discover.go, because the
// production code needs them too: gitRemoteURL and ghEnv both use them.

func setupGit(t *testing.T, dir string, args ...string) {
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

func gitOutput(t *testing.T, dir string, args ...string) string {
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

// TestGitRemoteURLIgnoresAnInheritedGitDir covers the PRODUCTION path.
// gitRemoteURL sets cmd.Dir and no cmd.Env, so an inherited GIT_DIR makes it
// report the WRONG repo's origin: repoTrawl would then classify, and act on,
// a repo it never looked at. Two repos with different origins make that
// concrete -- asking about B must answer B.
func TestGitRemoteURLIgnoresAnInheritedGitDir(t *testing.T) {
	mk := func(name, origin string) string {
		dir := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		setupGit(t, dir, "init", "-q")
		setupGit(t, dir, "remote", "add", "origin", origin)
		return dir
	}
	repoA := mk("a", "git@github.com:Org/AAA.git")
	repoB := mk("b", "git@github.com:Org/BBB.git")

	t.Setenv("GIT_DIR", filepath.Join(repoA, ".git"))

	got, err := gitRemoteURL(context.Background(), repoB)
	if err != nil {
		t.Fatalf("gitRemoteURL(repoB) failed: %v", err)
	}
	if got != "git@github.com:Org/BBB.git" {
		t.Fatalf("an inherited GIT_DIR redirected gitRemoteURL: asked about repoB, got %q", got)
	}
}

func TestDiscoverGitRunCannotWriteIntoAnInheritedGitDir(t *testing.T) {
	victim := t.TempDir()
	setupGit(t, victim, "init", "-q")
	setupGit(t, victim, "config", "user.email", "guard@example.invalid")
	setupGit(t, victim, "config", "user.name", "guard")
	if err := os.WriteFile(filepath.Join(victim, "real.txt"), []byte("real\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setupGit(t, victim, "add", "-A")
	setupGit(t, victim, "commit", "-qm", "real history")

	before := gitOutput(t, victim, "rev-list", "--count", "HEAD")

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

	if after := gitOutput(t, victim, "rev-list", "--count", "HEAD"); after != before {
		t.Fatalf("the suite wrote into the real repo through GIT_DIR: %s -> %s commits",
			before, after)
	}
	if tree := gitOutput(t, victim, "ls-tree", "-r", "HEAD", "--name-only"); tree != "real.txt" {
		t.Fatalf("real repo tree was replaced: %q", tree)
	}
}
