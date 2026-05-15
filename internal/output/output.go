package output

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Single-Molecule-Sequencing/repotrawl/internal/sync"
)

// Counters holds aggregate result counts.
type Counters struct {
	Pulled  int
	Cloned  int
	Skipped int
	Failed  int
	Partial int
}

// CountResults tallies results by outcome.
func CountResults(results []sync.Result) Counters {
	var c Counters
	for _, r := range results {
		switch {
		case r.Status == sync.StatusFailed:
			c.Failed++
		case r.Status == sync.StatusPartial:
			c.Partial++
		case r.Status == sync.StatusSkippedDirty || r.Status == sync.StatusSkippedDiverged:
			c.Skipped++
		case r.Action == sync.ActionClone:
			c.Cloned++
		default:
			c.Pulled++
		}
	}
	return c
}

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// StripANSI removes ANSI color codes from a string.
func StripANSI(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

func statusIcon(s sync.Status, color bool) string {
	switch s {
	case sync.StatusSuccess, sync.StatusUpToDate:
		if color {
			return "\033[32m✓\033[0m"
		}
		return "✓"
	case sync.StatusSkippedDirty, sync.StatusSkippedDiverged, sync.StatusPartial:
		if color {
			return "\033[33m⚠\033[0m"
		}
		return "⚠"
	case sync.StatusFailed:
		if color {
			return "\033[31m✗\033[0m"
		}
		return "✗"
	default:
		return "?"
	}
}

func actionOrder(r sync.Result) int {
	switch {
	case r.Action == sync.ActionClone && r.Status != sync.StatusFailed:
		return 0
	case r.Action == sync.ActionPull && (r.Status == sync.StatusSuccess || r.Status == sync.StatusUpToDate):
		return 1
	case r.Status == sync.StatusSkippedDirty || r.Status == sync.StatusSkippedDiverged:
		return 2
	case r.Status == sync.StatusFailed:
		return 3
	default:
		return 4
	}
}

func sortResults(results []sync.Result) {
	sort.Slice(results, func(i, j int) bool {
		oi, oj := actionOrder(results[i]), actionOrder(results[j])
		if oi != oj {
			return oi < oj
		}
		return strings.ToLower(results[i].RepoName) < strings.ToLower(results[j].RepoName)
	})
}

// RenderSummaryTable writes the compact summary table to w.
func RenderSummaryTable(w io.Writer, results []sync.Result, cfg Config, elapsedMs int64) {
	sorted := make([]sync.Result, len(results))
	copy(sorted, results)
	sortResults(sorted)

	totalRepos := len(sorted)
	newRepos := 0
	for _, r := range sorted {
		if r.Action == sync.ActionClone {
			newRepos++
		}
	}

	header := fmt.Sprintf("repotrawl — %s (%d repos", cfg.OrgName, totalRepos)
	if newRepos > 0 {
		header += fmt.Sprintf(", %d new", newRepos)
	}
	header += ")\n"
	fmt.Fprint(w, header)

	maxName := 4 // "REPO"
	for _, r := range sorted {
		if len(r.RepoName) > maxName {
			maxName = len(r.RepoName)
		}
	}
	if maxName > 40 {
		maxName = 40
	}

	fmt.Fprintf(w, "\n %-*s  %-6s  %s\n", maxName, "REPO", "ACTION", "STATUS")

	for _, r := range sorted {
		name := r.RepoName
		if len(name) > maxName {
			name = name[:maxName-1] + "…"
		}
		icon := statusIcon(r.Status, cfg.Color)
		fmt.Fprintf(w, " %-*s  %-6s  %s %s\n", maxName, name, r.Action, icon, r.Summary)
	}

	c := CountResults(sorted)
	elapsed := time.Duration(elapsedMs) * time.Millisecond

	fmt.Fprintf(w, "\n%s\n", strings.Repeat("━", maxName+30))

	var parts []string
	if c.Pulled > 0 {
		parts = append(parts, fmt.Sprintf("✓ %d pulled", c.Pulled))
	}
	if c.Cloned > 0 {
		parts = append(parts, fmt.Sprintf("✓ %d cloned", c.Cloned))
	}
	if c.Skipped > 0 {
		parts = append(parts, fmt.Sprintf("⚠ %d skipped", c.Skipped))
	}
	if c.Partial > 0 {
		parts = append(parts, fmt.Sprintf("⚠ %d partial", c.Partial))
	}
	if c.Failed > 0 {
		parts = append(parts, fmt.Sprintf("✗ %d failed", c.Failed))
	}
	fmt.Fprintf(w, " %s\n", strings.Join(parts, "  "))
	fmt.Fprintf(w, " Completed in %.1fs\n", elapsed.Seconds())
}

// RenderVerboseLine writes a single streaming progress line.
func RenderVerboseLine(w io.Writer, index, total int, r sync.Result, color bool) {
	icon := statusIcon(r.Status, color)
	padLen := 40 - len(r.RepoName)
	if padLen < 2 {
		padLen = 2
	}
	padding := strings.Repeat(".", padLen)
	fmt.Fprintf(w, "[%d/%d] %s %s %s %s\n", index, total, r.RepoName, padding, icon, r.Summary)
}

// RenderTraceLine writes full trace output for a repo.
func RenderTraceLine(w io.Writer, index, total int, r sync.Result) {
	fmt.Fprintf(w, "[%d/%d] %s\n", index, total, r.RepoName)
	if r.Output != "" {
		for _, line := range strings.Split(strings.TrimSpace(r.Output), "\n") {
			fmt.Fprintf(w, "  %s\n", line)
		}
	}
}
