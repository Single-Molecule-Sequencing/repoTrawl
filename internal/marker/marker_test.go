package marker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGitRepo creates a minimal directory that looks like a git clone (has a
// .git/ directory) so Reconcile treats it as a real repo.
func fakeGitRepo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(dir, ".git", "info"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	return dir
}

func TestReconcileMarksArchivedRepo(t *testing.T) {
	dir := fakeGitRepo(t, "old-repo")
	res := Reconcile([]State{{Name: "old-repo", Dir: dir, Archived: true, LastActivity: "2025-11-20"}}, "v0.3.0")

	if len(res) != 1 || res[0].Action != ActionMarked {
		t.Fatalf("want one marked result, got %+v", res)
	}
	// Both marker files exist.
	mdPath := filepath.Join(dir, MarkerFile)
	md, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("ARCHIVED.md not written: %v", err)
	}
	if !strings.Contains(string(md), "ARCHIVED — DO NOT USE") {
		t.Errorf("banner missing from ARCHIVED.md:\n%s", md)
	}
	if !strings.Contains(string(md), "2025-11-20") {
		t.Errorf("last_activity missing from ARCHIVED.md")
	}
	if strings.ContainsAny(string(md), "⛔🚫❌") {
		t.Errorf("marker must not contain emoji")
	}
	dot, err := os.ReadFile(filepath.Join(dir, DotFile))
	if err != nil {
		t.Fatalf(".archived not written: %v", err)
	}
	if !strings.Contains(string(dot), "archived=true") {
		t.Errorf(".archived missing archived=true: %q", dot)
	}
	// Markers are added to .git/info/exclude so they don't dirty the tree.
	excl, err := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	if err != nil {
		t.Fatalf("exclude not written: %v", err)
	}
	if !strings.Contains(string(excl), "/"+MarkerFile) || !strings.Contains(string(excl), "/"+DotFile) {
		t.Errorf("exclude missing marker entries:\n%s", excl)
	}
}

func TestReconcileIsIdempotent(t *testing.T) {
	dir := fakeGitRepo(t, "old-repo")
	st := []State{{Name: "old-repo", Dir: dir, Archived: true, LastActivity: "2025-11-20"}}

	first := Reconcile(st, "v0.3.0")
	if len(first) != 1 || first[0].Action != ActionMarked {
		t.Fatalf("first run should mark, got %+v", first)
	}
	second := Reconcile(st, "v0.3.0")
	if len(second) != 0 {
		t.Errorf("second run should be a noop, got %+v", second)
	}
	// exclude must not have duplicate entries after two runs.
	excl, _ := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	if n := strings.Count(string(excl), "/"+MarkerFile); n != 1 {
		t.Errorf("exclude has %d ARCHIVED.md entries, want 1", n)
	}
}

func TestReconcileUnmarksWhenUnarchived(t *testing.T) {
	dir := fakeGitRepo(t, "revived")
	Reconcile([]State{{Name: "revived", Dir: dir, Archived: true}}, "v0.3.0")
	if _, err := os.Stat(filepath.Join(dir, MarkerFile)); err != nil {
		t.Fatalf("precondition: marker should exist")
	}
	// Now the repo is un-archived.
	res := Reconcile([]State{{Name: "revived", Dir: dir, Archived: false}}, "v0.3.0")
	if len(res) != 1 || res[0].Action != ActionUnmarked {
		t.Fatalf("want unmarked, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, MarkerFile)); !os.IsNotExist(err) {
		t.Errorf("ARCHIVED.md should be removed after un-archive")
	}
	if _, err := os.Stat(filepath.Join(dir, DotFile)); !os.IsNotExist(err) {
		t.Errorf(".archived should be removed after un-archive")
	}
}

func TestReconcileSkipsActiveAndNonRepos(t *testing.T) {
	active := fakeGitRepo(t, "active")
	plain := filepath.Join(t.TempDir(), "not-a-repo") // no .git
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	res := Reconcile([]State{
		{Name: "active", Dir: active, Archived: false},   // active git repo: noop
		{Name: "not-a-repo", Dir: plain, Archived: true}, // not a git repo: skipped
		{Name: "missing", Dir: "", Archived: true},       // no dir: skipped
	}, "v0.3.0")
	if len(res) != 0 {
		t.Errorf("expected no actions, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(plain, MarkerFile)); !os.IsNotExist(err) {
		t.Errorf("must not write markers into a non-git directory")
	}
}

func TestUnknownLastActivity(t *testing.T) {
	dir := fakeGitRepo(t, "no-date")
	Reconcile([]State{{Name: "no-date", Dir: dir, Archived: true}}, "v0.3.0")
	md, _ := os.ReadFile(filepath.Join(dir, MarkerFile))
	if !strings.Contains(string(md), "last_activity:  unknown") {
		t.Errorf("missing last_activity fallback:\n%s", md)
	}
}

// TestForeignMarkerFileNotOverwritten guards against data loss: a pre-existing
// ARCHIVED.md that repoTrawl did NOT generate (no sentinel) must never be
// overwritten, the sibling marker must NOT be written, the exclude block must NOT
// be added, and the repo is reported as skipped (all-or-nothing).
func TestForeignMarkerFileNotOverwritten(t *testing.T) {
	dir := fakeGitRepo(t, "has-foreign-md")
	foreign := "# My own archive notes\nKeep this, it is mine.\n"
	mdPath := filepath.Join(dir, MarkerFile)
	if err := os.WriteFile(mdPath, []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}

	res := Reconcile([]State{{Name: "has-foreign-md", Dir: dir, Archived: true}}, "v0.3.0")
	if len(res) != 1 || res[0].Action != ActionSkipped {
		t.Fatalf("want one skipped result, got %+v", res)
	}
	got, _ := os.ReadFile(mdPath)
	if string(got) != foreign {
		t.Errorf("foreign ARCHIVED.md was modified:\n%s", got)
	}
	// All-or-nothing: the sibling .archived must not be written...
	if _, err := os.Stat(filepath.Join(dir, DotFile)); !os.IsNotExist(err) {
		t.Errorf(".archived should not be written when ARCHIVED.md is foreign")
	}
	// ...and the exclude block must not be added.
	if b, _ := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude")); strings.Contains(string(b), excludeBegin) {
		t.Errorf("exclude block should not be added when a marker is foreign:\n%s", b)
	}
}

// TestForeignSiblingBlocksMarking is the symmetric case: a foreign .archived must
// also block marking entirely (ARCHIVED.md not written, exclude not added).
func TestForeignSiblingBlocksMarking(t *testing.T) {
	dir := fakeGitRepo(t, "has-foreign-sibling")
	foreign := "hand-written, not repoTrawl\n"
	if err := os.WriteFile(filepath.Join(dir, DotFile), []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}

	res := Reconcile([]State{{Name: "has-foreign-sibling", Dir: dir, Archived: true}}, "v0.3.0")
	if len(res) != 1 || res[0].Action != ActionSkipped {
		t.Fatalf("want one skipped result, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, MarkerFile)); !os.IsNotExist(err) {
		t.Errorf("ARCHIVED.md should not be written when .archived is foreign")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude")); strings.Contains(string(b), excludeBegin) {
		t.Errorf("exclude block should not be added when a marker is foreign")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, DotFile)); string(got) != foreign {
		t.Errorf("foreign .archived was modified: %q", got)
	}
}

// TestForeignDotFileNotDeleted guards the unmark path: a foreign .archived file
// must never be deleted when a repo is (or becomes) un-archived.
func TestForeignDotFileNotDeleted(t *testing.T) {
	dir := fakeGitRepo(t, "has-foreign-dot")
	foreign := "this is not a repoTrawl marker\n"
	dotPath := filepath.Join(dir, DotFile)
	if err := os.WriteFile(dotPath, []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}

	res := Reconcile([]State{{Name: "has-foreign-dot", Dir: dir, Archived: false}}, "v0.3.0")
	if len(res) != 0 {
		t.Errorf("un-archive of repo with only a foreign .archived should be a noop, got %+v", res)
	}
	got, err := os.ReadFile(dotPath)
	if err != nil || string(got) != foreign {
		t.Errorf("foreign .archived was modified or deleted: err=%v content=%q", err, got)
	}
}

// TestUnarchiveRemovesExcludeBlock verifies the managed exclude block is removed
// (not left stale) when a repo is un-archived.
func TestUnarchiveRemovesExcludeBlock(t *testing.T) {
	dir := fakeGitRepo(t, "toggle")
	Reconcile([]State{{Name: "toggle", Dir: dir, Archived: true}}, "v0.3.0")
	exclPath := filepath.Join(dir, ".git", "info", "exclude")
	if b, _ := os.ReadFile(exclPath); !strings.Contains(string(b), excludeBegin) {
		t.Fatalf("precondition: exclude block should be present")
	}

	Reconcile([]State{{Name: "toggle", Dir: dir, Archived: false}}, "v0.3.0")
	b, _ := os.ReadFile(exclPath)
	if strings.Contains(string(b), excludeBegin) || strings.Contains(string(b), excludeEnd) {
		t.Errorf("managed exclude block should be removed after un-archive:\n%s", b)
	}
}

// TestExcludeBlockNewlineSafe verifies that appending the managed block to a
// pre-existing exclude file without a trailing newline does not concatenate onto
// the last line, and preserves the original content.
func TestExcludeBlockNewlineSafe(t *testing.T) {
	dir := fakeGitRepo(t, "no-newline-exclude")
	exclPath := filepath.Join(dir, ".git", "info", "exclude")
	if err := os.WriteFile(exclPath, []byte("*.log"), 0o644); err != nil { // no trailing newline
		t.Fatal(err)
	}

	Reconcile([]State{{Name: "no-newline-exclude", Dir: dir, Archived: true}}, "v0.3.0")
	b, _ := os.ReadFile(exclPath)
	s := string(b)
	if !strings.Contains(s, "*.log\n"+excludeBegin) {
		t.Errorf("original exclude line should be preserved on its own line:\n%s", s)
	}
	if strings.Contains(s, "*.log#") {
		t.Errorf("managed block concatenated onto prior line:\n%s", s)
	}
}

// TestGitFileWorktreeSkipped verifies repos whose .git is a FILE (worktrees /
// submodules) are skipped — we cannot manage their info/exclude, so writing
// markers would dirty the tree.
func TestGitFileWorktreeSkipped(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /elsewhere/.git/worktrees/wt\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := Reconcile([]State{{Name: "worktree", Dir: dir, Archived: true}}, "v0.3.0")
	if len(res) != 0 {
		t.Errorf("worktree (.git file) should be skipped, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, MarkerFile)); !os.IsNotExist(err) {
		t.Errorf("must not write markers into a worktree clone")
	}
}

// TestExcludeBlockSelfHealsDanglingBegin verifies a corrupt managed block with a
// begin delimiter but no end is repaired into exactly one complete block.
func TestExcludeBlockSelfHealsDanglingBegin(t *testing.T) {
	dir := fakeGitRepo(t, "dangling")
	exclPath := filepath.Join(dir, ".git", "info", "exclude")
	// A begin delimiter and an entry, but no end (e.g. a truncated prior write).
	if err := os.WriteFile(exclPath, []byte("*.tmp\n"+excludeBegin+"\n/"+MarkerFile+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	Reconcile([]State{{Name: "dangling", Dir: dir, Archived: true}}, "v0.3.0")
	b, _ := os.ReadFile(exclPath)
	s := string(b)
	if n := strings.Count(s, excludeBegin); n != 1 {
		t.Errorf("want exactly 1 begin delimiter, got %d:\n%s", n, s)
	}
	if n := strings.Count(s, excludeEnd); n != 1 {
		t.Errorf("want exactly 1 end delimiter, got %d:\n%s", n, s)
	}
	if strings.Index(s, excludeEnd) < strings.Index(s, excludeBegin) {
		t.Errorf("delimiters out of order after self-heal:\n%s", s)
	}
	if !strings.Contains(s, "*.tmp") {
		t.Errorf("unrelated exclude content lost during self-heal:\n%s", s)
	}
}

// TestRemoveExcludeBlockEndBeforeBegin verifies the end delimiter is matched only
// AFTER the begin delimiter, so a stray earlier end can't cause a wrong-region
// slice that drops unrelated content.
func TestRemoveExcludeBlockEndBeforeBegin(t *testing.T) {
	dir := fakeGitRepo(t, "weird")
	exclPath := filepath.Join(dir, ".git", "info", "exclude")
	content := excludeEnd + "\n*.log\n" + excludeBegin + "\n/" + MarkerFile + "\n/" + DotFile + "\n" + excludeEnd + "\n"
	if err := os.WriteFile(exclPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := removeExcludeBlock(dir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(exclPath)
	s := string(b)
	if strings.Contains(s, "/"+MarkerFile) || strings.Contains(s, excludeBegin) {
		t.Errorf("managed entries should be removed:\n%s", s)
	}
	if !strings.Contains(s, "*.log") {
		t.Errorf("unrelated content before the block must be preserved:\n%s", s)
	}
}
