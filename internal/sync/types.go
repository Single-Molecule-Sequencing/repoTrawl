package sync

import "fmt"

// Action describes the operation to perform on a repo.
type Action int

const (
	ActionPull Action = iota
	ActionClone
	ActionSkip
)

func (a Action) String() string {
	switch a {
	case ActionPull:
		return "pull"
	case ActionClone:
		return "clone"
	case ActionSkip:
		return "skip"
	default:
		return "unknown"
	}
}

// Status describes the outcome of a sync operation.
type Status int

const (
	StatusSuccess Status = iota
	StatusUpToDate
	StatusSkippedDirty
	StatusSkippedDiverged
	StatusPartial
	StatusFailed
)

func (s Status) String() string {
	switch s {
	case StatusSuccess:
		return "success"
	case StatusUpToDate:
		return "up to date"
	case StatusSkippedDirty:
		return "dirty"
	case StatusSkippedDiverged:
		return "diverged"
	case StatusPartial:
		return "partial"
	case StatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Task represents a single unit of work for the worker pool.
type Task struct {
	RepoName string
	Action   Action
	CloneURL string // only for ActionClone
	LocalDir string // absolute path to the repo directory
}

// Result holds the outcome of executing a Task.
type Result struct {
	RepoName string
	Action   Action
	Status   Status
	Summary  string // human-readable detail
	Output   string // full git output (for trace mode)
}

// ProgressFunc is called each time a result is available.
type ProgressFunc func(index, total int, result Result)

// String returns a debug representation of a Task.
func (t Task) String() string {
	return fmt.Sprintf("%s:%s", t.Action, t.RepoName)
}
