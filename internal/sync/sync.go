package sync

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	gosync "sync"
	"time"
)

const (
	// pullTimeout is the deadline for a single git pull + submodule update.
	pullTimeout = 2 * time.Minute
	// cloneTimeout is the deadline for a single git clone + submodule update.
	cloneTimeout = 10 * time.Minute
)

// StatusTimeout is the deadline for git status --porcelain.
// Default is 60s to accommodate WSL/NTFS cross-filesystem latency on large repos.
// Override via the -status-timeout CLI flag.
var StatusTimeout = 60 * time.Second

// useHTTPSInsteadOfSSH is set once at startup by detecting whether the user
// authenticates to GitHub over HTTPS (via gh or credential helper) rather than SSH.
var useHTTPSInsteadOfSSH = detectHTTPSAuth()

// detectHTTPSAuth returns true if the user's GitHub auth is HTTPS-based,
// meaning SSH-style submodule URLs (git@github.com:...) would fail without
// a URL rewrite.
func detectHTTPSAuth() bool {
	// Check if gh CLI is authenticated with HTTPS protocol.
	cmd := exec.Command("gh", "auth", "status")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// gh not installed or not authenticated — can't determine, assume SSH might work.
		return false
	}
	// gh auth status prints the protocol (https or ssh).
	// If it mentions "https" as the git protocol, SSH URLs will likely fail.
	return strings.Contains(strings.ToLower(string(out)), "git protocol: https")
}

// redirectingGitVars can point git at a repository other than the one implied
// by cmd.Dir. GIT_DIR outranks cmd.Dir, and with GIT_DIR set but GIT_WORK_TREE
// unset git treats the CURRENT directory as the work tree. repoTrawl runs git
// across many clones and distinguishes them ONLY by cmd.Dir, so inheriting
// these would silently aim every operation at one repo.
//
// Not hypothetical: the same mechanism destroyed lab-system's main on
// 2026-08-15, when a pre-push hook exported GIT_DIR into a test suite.
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
// Everything else is passed through: git still needs PATH, HOME and the rest.
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

// gitEnv returns environment variables that suppress interactive prompts
// and, when HTTPS auth is detected, rewrite SSH URLs to HTTPS.
func gitEnv() []string {
	env := scrubbedEnviron()
	env = append(env,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_SSH_COMMAND=ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new",
	)
	if useHTTPSInsteadOfSSH {
		env = append(env,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=url.https://github.com/.insteadOf",
			"GIT_CONFIG_VALUE_0=git@github.com:",
		)
	}
	return env
}

// IsDirty returns true if the repo at dir has uncommitted changes.
func IsDirty(ctx context.Context, dir string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, StatusTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	cmd.Dir = dir
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return false, fmt.Errorf("git status timed out after %s", StatusTimeout)
	}
	if err != nil {
		return false, fmt.Errorf("git status: %w", err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// GitPull runs git pull --ff-only in the given directory.
func GitPull(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "pull", "--ff-only")
	cmd.Dir = dir
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("git pull timed out")
	}
	return string(out), err
}

// GitClone clones a repo into the given directory (full clone, no shallow).
func GitClone(ctx context.Context, url, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "clone", url, dir)
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("git clone timed out")
	}
	return string(out), err
}

// GitSubmoduleUpdate runs git submodule update --init --recursive.
func GitSubmoduleUpdate(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "submodule", "update", "--init", "--recursive")
	cmd.Dir = dir
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("submodule update timed out")
	}
	return string(out), err
}

// classifyPullError maps a failed `git pull --ff-only` to a Status and summary.
// Two outcomes are treated as non-fatal skips (⚠) rather than failures (✗),
// consistent with repoTrawl's safe-by-default philosophy (it already skips
// dirty repos):
//   - history that has diverged and cannot fast-forward, and
//   - a tracked upstream branch that does not exist on the remote — an empty
//     repository with no default branch, or a branch deleted/renamed upstream.
//     Git reports this as "no such ref was fetched".
//
// Anything else is a genuine failure.
func classifyPullError(pullOut string, pullErr error) (Status, string) {
	switch {
	case strings.Contains(pullOut, "divergent") ||
		strings.Contains(pullOut, "Not possible to fast-forward") ||
		strings.Contains(pullOut, "not possible to fast-forward"):
		return StatusSkippedDiverged, "local branch has diverged"
	case strings.Contains(pullOut, "no such ref was fetched"):
		return StatusSkippedNoUpstream, "no upstream branch"
	default:
		summary := strings.TrimSpace(pullOut)
		if summary == "" {
			summary = pullErr.Error()
		}
		return StatusFailed, summary
	}
}

// ParsePullSummary extracts a human-readable summary from git pull output.
func ParsePullSummary(output string) string {
	output = strings.TrimSpace(output)
	if output == "" || output == "Already up to date." || output == "Already up-to-date." {
		return "up to date"
	}

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "file") && strings.Contains(line, "changed") {
			return line
		}
	}

	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "Fast-forward") {
			return "updated (fast-forward)"
		}
	}

	return "updated"
}

// RunPool executes tasks in parallel using a goroutine worker pool.
func RunPool(ctx context.Context, tasks []Task, jobs int, onProgress ProgressFunc) []Result {
	if len(tasks) == 0 {
		return nil
	}

	taskCh := make(chan indexedTask, len(tasks))
	resultCh := make(chan indexedResult, len(tasks))

	var wg gosync.WaitGroup
	for i := 0; i < jobs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range taskCh {
				select {
				case <-ctx.Done():
					resultCh <- indexedResult{
						index: it.index,
						result: Result{
							RepoName: it.task.RepoName,
							Action:   it.task.Action,
							Status:   StatusFailed,
							Summary:  "cancelled",
						},
					}
				default:
					resultCh <- indexedResult{
						index:  it.index,
						result: executeTask(ctx, it.task),
					}
				}
			}
		}()
	}

	for i, task := range tasks {
		taskCh <- indexedTask{index: i, task: task}
	}
	close(taskCh)

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	results := make([]Result, len(tasks))
	completed := 0
	for ir := range resultCh {
		results[ir.index] = ir.result
		completed++
		if onProgress != nil {
			onProgress(completed, len(tasks), ir.result)
		}
	}

	return results
}

type indexedTask struct {
	index int
	task  Task
}

type indexedResult struct {
	index  int
	result Result
}

func executeTask(ctx context.Context, task Task) Result {
	switch task.Action {
	case ActionPull:
		return executePull(ctx, task)
	case ActionClone:
		return executeClone(ctx, task)
	default:
		return Result{
			RepoName: task.RepoName,
			Action:   task.Action,
			Status:   StatusFailed,
			Summary:  "unknown action",
		}
	}
}

func executePull(ctx context.Context, task Task) Result {
	r := Result{RepoName: task.RepoName, Action: ActionPull}

	ctx, cancel := context.WithTimeout(ctx, pullTimeout)
	defer cancel()

	dirty, err := IsDirty(ctx, task.LocalDir)
	if err != nil {
		r.Status = StatusFailed
		r.Summary = fmt.Sprintf("status check failed: %v", err)
		return r
	}
	if dirty {
		r.Status = StatusSkippedDirty
		r.Summary = "uncommitted changes"
		return r
	}

	pullOut, err := GitPull(ctx, task.LocalDir)
	r.Output = pullOut
	if err != nil {
		r.Status, r.Summary = classifyPullError(pullOut, err)
		return r
	}

	summary := ParsePullSummary(pullOut)
	if summary == "up to date" {
		r.Status = StatusUpToDate
	} else {
		r.Status = StatusSuccess
	}
	r.Summary = summary

	subOut, err := GitSubmoduleUpdate(ctx, task.LocalDir)
	r.Output += subOut
	if err != nil {
		r.Status = StatusPartial
		r.Summary += " (submodule update failed)"
	}

	return r
}

func executeClone(ctx context.Context, task Task) Result {
	r := Result{RepoName: task.RepoName, Action: ActionClone}

	ctx, cancel := context.WithTimeout(ctx, cloneTimeout)
	defer cancel()

	cloneOut, err := GitClone(ctx, task.CloneURL, task.LocalDir)
	r.Output = cloneOut
	if err != nil {
		r.Status = StatusFailed
		summary := strings.TrimSpace(cloneOut)
		if summary == "" {
			summary = err.Error()
		}
		r.Summary = summary
		return r
	}

	r.Status = StatusSuccess
	r.Summary = "cloned"

	subOut, err := GitSubmoduleUpdate(ctx, task.LocalDir)
	r.Output += subOut
	if err != nil {
		r.Status = StatusPartial
		r.Summary = "cloned (submodule update failed)"
	}

	return r
}
