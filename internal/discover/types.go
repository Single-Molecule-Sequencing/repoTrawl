package discover

// RepoInfo represents a repository discovered from the GitHub API.
type RepoInfo struct {
	Name     string
	CloneURL string // HTTPS clone URL from the API
	Org      string
	Archived bool
	Fork     bool
}

// LocalRepo represents a git repository found on the local filesystem.
type LocalRepo struct {
	Name      string
	Path      string
	RemoteURL string
	Org       string
	Protocol  string // "ssh" or "https"
}

// ScanResult holds the output of scanning a directory for repos and orgs.
type ScanResult struct {
	LocalRepos []LocalRepo
	Orgs       []string  // deduplicated orgs found
	Protocol   string    // majority protocol detected
	Warnings   []string
}

// ClassifiedTask is the output of classification, consumed by the sync package.
type ClassifiedTask struct {
	RepoName string
	Action   string // "pull", "clone"
	CloneURL string
	LocalDir string
}
