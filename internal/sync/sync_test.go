package sync

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIsDirty(t *testing.T) {
	tmp := t.TempDir()
	gitRun(t, tmp, "git", "init")
	gitRun(t, tmp, "git", "commit", "--allow-empty", "-m", "init")

	ctx := context.Background()

	dirty, err := IsDirty(ctx, tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dirty {
		t.Error("expected clean repo, got dirty")
	}

	os.WriteFile(filepath.Join(tmp, "file.txt"), []byte("hello"), 0o644)

	dirty, err = IsDirty(ctx, tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !dirty {
		t.Error("expected dirty repo, got clean")
	}
}

func TestParsePullSummary(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{"already up to date", "Already up to date.", "up to date"},
		{"already up-to-date", "Already up-to-date.", "up to date"},
		{"empty", "", "up to date"},
		{
			"fast-forward with stats",
			"Updating abc..def\nFast-forward\n README.md | 2 +-\n 1 file changed, 1 insertion(+), 1 deletion(-)",
			"1 file changed, 1 insertion(+), 1 deletion(-)",
		},
		{
			"fast-forward no stats",
			"Updating abc..def\nFast-forward\n",
			"updated (fast-forward)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParsePullSummary(tt.output)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClassifyPullError(t *testing.T) {
	tests := []struct {
		name       string
		pullOut    string
		wantStatus Status
		wantSum    string
	}{
		{
			"diverged",
			"hint: Not possible to fast-forward, aborting.",
			StatusSkippedDiverged,
			"local branch has diverged",
		},
		{
			"empty or deleted upstream",
			"Your configuration specifies to merge with the ref 'refs/heads/main' " +
				"from the remote, but no such ref was fetched.",
			StatusSkippedNoUpstream,
			"no upstream branch",
		},
		{
			"genuine failure surfaces git output",
			"fatal: unable to access remote: Could not resolve host",
			StatusFailed,
			"fatal: unable to access remote: Could not resolve host",
		},
		{
			"empty output falls back to the error string",
			"",
			StatusFailed,
			"boom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStatus, gotSum := classifyPullError(tt.pullOut, errors.New("boom"))
			if gotStatus != tt.wantStatus {
				t.Errorf("status = %s, want %s", gotStatus, tt.wantStatus)
			}
			if gotSum != tt.wantSum {
				t.Errorf("summary = %q, want %q", gotSum, tt.wantSum)
			}
		})
	}
}

func TestRunPool_PullClean(t *testing.T) {
	tmp := t.TempDir()

	bareDir := filepath.Join(tmp, "test-repo-bare.git")
	gitRun(t, tmp, "git", "init", "--bare", bareDir)

	repoDir := filepath.Join(tmp, "test-repo")
	gitRun(t, tmp, "git", "clone", bareDir, repoDir)

	os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# test"), 0o644)
	gitRun(t, repoDir, "git", "add", ".")
	gitRun(t, repoDir, "git", "commit", "-m", "init")
	gitRun(t, repoDir, "git", "push")

	tasks := []Task{
		{RepoName: "test-repo", Action: ActionPull, LocalDir: repoDir},
	}

	results := RunPool(context.Background(), tasks, 2, nil)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != StatusUpToDate && results[0].Status != StatusSuccess {
		t.Errorf("expected success/up-to-date, got %s: %s", results[0].Status, results[0].Summary)
	}
}

func TestRunPool_DirtySkip(t *testing.T) {
	tmp := t.TempDir()

	bareDir := filepath.Join(tmp, "dirty-repo-bare.git")
	gitRun(t, tmp, "git", "init", "--bare", bareDir)

	repoDir := filepath.Join(tmp, "dirty-repo")
	gitRun(t, tmp, "git", "clone", bareDir, repoDir)

	os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# test"), 0o644)
	gitRun(t, repoDir, "git", "add", ".")
	gitRun(t, repoDir, "git", "commit", "-m", "init")
	gitRun(t, repoDir, "git", "push")

	os.WriteFile(filepath.Join(repoDir, "dirty.txt"), []byte("uncommitted"), 0o644)

	tasks := []Task{
		{RepoName: "dirty-repo", Action: ActionPull, LocalDir: repoDir},
	}

	results := RunPool(context.Background(), tasks, 2, nil)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != StatusSkippedDirty {
		t.Errorf("expected dirty skip, got %s: %s", results[0].Status, results[0].Summary)
	}
}

func TestRunPool_NoUpstreamSkip(t *testing.T) {
	tmp := t.TempDir()

	bareDir := filepath.Join(tmp, "noup-bare.git")
	gitRun(t, tmp, "git", "init", "--bare", "-b", "main", bareDir)

	repoDir := filepath.Join(tmp, "noup-repo")
	gitRun(t, tmp, "git", "clone", bareDir, repoDir)

	// Establish a tracked main on the remote, then delete it there so the local
	// checkout tracks an upstream ref that no longer exists — the same condition
	// an empty repo (no default branch) or a deleted/renamed upstream branch
	// produces: git pull fails with "no such ref was fetched".
	os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# test"), 0o644)
	gitRun(t, repoDir, "git", "add", ".")
	gitRun(t, repoDir, "git", "commit", "-m", "init")
	gitRun(t, repoDir, "git", "push", "-u", "origin", "main")
	gitRun(t, tmp, "git", "--git-dir", bareDir, "update-ref", "-d", "refs/heads/main")

	tasks := []Task{
		{RepoName: "noup-repo", Action: ActionPull, LocalDir: repoDir},
	}

	results := RunPool(context.Background(), tasks, 2, nil)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != StatusSkippedNoUpstream {
		t.Errorf("expected no-upstream skip, got %s: %s", results[0].Status, results[0].Summary)
	}
}

func TestRunPool_Clone(t *testing.T) {
	tmp := t.TempDir()

	bareDir := filepath.Join(tmp, "clone-source.git")
	gitRun(t, tmp, "git", "init", "--bare", bareDir)

	seedDir := filepath.Join(tmp, "seed")
	gitRun(t, tmp, "git", "clone", bareDir, seedDir)
	os.WriteFile(filepath.Join(seedDir, "README.md"), []byte("# test"), 0o644)
	gitRun(t, seedDir, "git", "add", ".")
	gitRun(t, seedDir, "git", "commit", "-m", "init")
	gitRun(t, seedDir, "git", "push")

	cloneTarget := filepath.Join(tmp, "cloned-repo")

	tasks := []Task{
		{RepoName: "cloned-repo", Action: ActionClone, CloneURL: bareDir, LocalDir: cloneTarget},
	}

	results := RunPool(context.Background(), tasks, 2, nil)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != StatusSuccess {
		t.Errorf("expected success, got %s: %s", results[0].Status, results[0].Summary)
	}

	if _, err := os.Stat(filepath.Join(cloneTarget, "README.md")); err != nil {
		t.Errorf("cloned repo should contain README.md: %v", err)
	}
}

func TestGitEnv_HTTPSRewrite(t *testing.T) {
	env := gitEnv()

	// Verify core env vars are always present.
	found := false
	for _, e := range env {
		if e == "GIT_TERMINAL_PROMPT=0" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected GIT_TERMINAL_PROMPT=0 in gitEnv()")
	}

	// When useHTTPSInsteadOfSSH is true, the rewrite config vars must be present.
	origVal := useHTTPSInsteadOfSSH
	defer func() { useHTTPSInsteadOfSSH = origVal }()

	useHTTPSInsteadOfSSH = true
	env = gitEnv()
	var hasCount, hasKey, hasValue bool
	for _, e := range env {
		switch e {
		case "GIT_CONFIG_COUNT=1":
			hasCount = true
		case "GIT_CONFIG_KEY_0=url.https://github.com/.insteadOf":
			hasKey = true
		case "GIT_CONFIG_VALUE_0=git@github.com:":
			hasValue = true
		}
	}
	if !hasCount || !hasKey || !hasValue {
		t.Errorf("expected HTTPS rewrite env vars when useHTTPSInsteadOfSSH=true, got count=%v key=%v value=%v", hasCount, hasKey, hasValue)
	}

	// When false, the rewrite key should not be injected by gitEnv.
	useHTTPSInsteadOfSSH = false
	env = gitEnv()
	for _, e := range env {
		if e == "GIT_CONFIG_KEY_0=url.https://github.com/.insteadOf" {
			t.Error("unexpected SSH-to-HTTPS rewrite key when useHTTPSInsteadOfSSH=false")
		}
	}
}

func gitRun(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	// scrubbedEnviron, not os.Environ: an inherited GIT_DIR outranks cmd.Dir,
	// so without this every "hermetic" test below writes into whatever repo the
	// caller was pushing from. That is the mechanism that destroyed
	// lab-system's main on 2026-08-15. See gitdir_leak_test.go.
	cmd.Env = append(scrubbedEnviron(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("command %s %v failed: %s\n%s", name, args, err, out)
	}
}
