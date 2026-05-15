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
	// statusTimeout is the deadline for git status --porcelain.
	statusTimeout = 15 * time.Second
)

// gitEnv returns environment variables that suppress interactive prompts.
func gitEnv() []string {
	env := os.Environ()
	env = append(env,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_SSH_COMMAND=ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new",
	)
	return env
}

// IsDirty returns true if the repo at dir has uncommitted changes.
func IsDirty(ctx context.Context, dir string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	cmd.Dir = dir
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return false, fmt.Errorf("git status timed out after %s", statusTimeout)
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
		if strings.Contains(pullOut, "divergent") ||
			strings.Contains(pullOut, "Not possible to fast-forward") ||
			strings.Contains(pullOut, "not possible to fast-forward") {
			r.Status = StatusSkippedDiverged
			r.Summary = "local branch has diverged"
		} else {
			r.Status = StatusFailed
			r.Summary = strings.TrimSpace(pullOut)
		}
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
		r.Summary = strings.TrimSpace(cloneOut)
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
