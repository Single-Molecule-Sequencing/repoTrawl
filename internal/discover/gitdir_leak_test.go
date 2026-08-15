package discover

import (
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

var redirectingGitVars = []string{
	"GIT_DIR",
	"GIT_WORK_TREE",
	"GIT_INDEX_FILE",
	"GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES",
	"GIT_COMMON_DIR",
	"GIT_NAMESPACE",
}

// scrubbedEnviron is os.Environ() minus every repo-redirecting git variable.
func scrubbedEnviron() []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		drop := false
		for _, name := range redirectingGitVars {
			if strings.HasPrefix(kv, name+"=") {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}

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
