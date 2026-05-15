package output

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Single-Molecule-Sequencing/repotrawl/internal/sync"
)

func TestCountResults(t *testing.T) {
	results := []sync.Result{
		{Status: sync.StatusSuccess, Action: sync.ActionPull},
		{Status: sync.StatusUpToDate, Action: sync.ActionPull},
		{Status: sync.StatusSuccess, Action: sync.ActionClone},
		{Status: sync.StatusSkippedDirty, Action: sync.ActionPull},
		{Status: sync.StatusFailed, Action: sync.ActionPull},
		{Status: sync.StatusPartial, Action: sync.ActionPull},
	}

	c := CountResults(results)
	if c.Pulled != 2 {
		t.Errorf("pulled = %d, want 2", c.Pulled)
	}
	if c.Cloned != 1 {
		t.Errorf("cloned = %d, want 1", c.Cloned)
	}
	if c.Skipped != 1 {
		t.Errorf("skipped = %d, want 1", c.Skipped)
	}
	if c.Failed != 1 {
		t.Errorf("failed = %d, want 1", c.Failed)
	}
	if c.Partial != 1 {
		t.Errorf("partial = %d, want 1", c.Partial)
	}
}

func TestStripANSI(t *testing.T) {
	input := "\033[32m✓\033[0m success"
	got := StripANSI(input)
	want := "✓ success"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderSummaryTable_Sorting(t *testing.T) {
	results := []sync.Result{
		{RepoName: "repo-b", Action: sync.ActionPull, Status: sync.StatusUpToDate, Summary: "up to date"},
		{RepoName: "repo-a", Action: sync.ActionPull, Status: sync.StatusSuccess, Summary: "3 files changed"},
		{RepoName: "repo-d", Action: sync.ActionClone, Status: sync.StatusSuccess, Summary: "cloned"},
		{RepoName: "repo-c", Action: sync.ActionPull, Status: sync.StatusSkippedDirty, Summary: "uncommitted changes"},
	}

	var buf bytes.Buffer
	cfg := Config{Verbosity: VerbositySummary, Color: false, OrgName: "TestOrg"}
	RenderSummaryTable(&buf, results, cfg, 1500)

	output := buf.String()

	// Clones before pulls before skips
	cloneIdx := strings.Index(output, "repo-d")
	pullIdx := strings.Index(output, "repo-a")
	skipIdx := strings.Index(output, "repo-c")

	if cloneIdx == -1 || pullIdx == -1 || skipIdx == -1 {
		t.Fatalf("expected all repos in output:\n%s", output)
	}

	if cloneIdx > pullIdx {
		t.Error("clones should appear before pulls")
	}
	if pullIdx > skipIdx {
		t.Error("pulls should appear before skips")
	}

	// Alphabetical within group
	pullAIdx := strings.Index(output, "repo-a")
	pullBIdx := strings.Index(output, "repo-b")
	if pullAIdx > pullBIdx {
		t.Error("repo-a should appear before repo-b (alphabetical)")
	}

	if !strings.Contains(output, "pulled") {
		t.Error("should contain pull count in footer")
	}
	if !strings.Contains(output, "cloned") {
		t.Error("should contain clone count in footer")
	}
}

func TestRenderSummaryTable_Header(t *testing.T) {
	results := []sync.Result{
		{RepoName: "repo-a", Action: sync.ActionPull, Status: sync.StatusSuccess, Summary: "updated"},
		{RepoName: "new-repo", Action: sync.ActionClone, Status: sync.StatusSuccess, Summary: "cloned"},
	}

	var buf bytes.Buffer
	cfg := Config{Verbosity: VerbositySummary, Color: false, OrgName: "MyOrg"}
	RenderSummaryTable(&buf, results, cfg, 500)

	output := buf.String()
	if !strings.Contains(output, "MyOrg") {
		t.Error("header should contain org name")
	}
	if !strings.Contains(output, "2 repos") {
		t.Error("header should contain repo count")
	}
	if !strings.Contains(output, "1 new") {
		t.Error("header should show new repo count")
	}
}

func TestRenderVerboseLine(t *testing.T) {
	var buf bytes.Buffer
	r := sync.Result{
		RepoName: "smaseq",
		Action:   sync.ActionPull,
		Status:   sync.StatusUpToDate,
		Summary:  "up to date",
	}

	RenderVerboseLine(&buf, 1, 85, r, false)
	output := buf.String()

	if !strings.Contains(output, "[1/85]") {
		t.Error("should contain progress counter")
	}
	if !strings.Contains(output, "smaseq") {
		t.Error("should contain repo name")
	}
	if !strings.Contains(output, "✓") {
		t.Error("should contain success icon")
	}
}
