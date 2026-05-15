package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	remoteURLTimeout = 5 * time.Second
	ghAuthTimeout    = 10 * time.Second
	ghListTimeout    = 60 * time.Second
)

// ParseRemoteURL extracts the org, repo name, and protocol from a GitHub remote URL.
func ParseRemoteURL(rawURL string) (org, repo, protocol string, err error) {
	if rawURL == "" {
		return "", "", "", fmt.Errorf("empty URL")
	}

	if strings.HasPrefix(rawURL, "git@github.com:") {
		path := strings.TrimPrefix(rawURL, "git@github.com:")
		path = strings.TrimSuffix(path, ".git")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", "", "", fmt.Errorf("malformed SSH URL: %s", rawURL)
		}
		return parts[0], parts[1], "ssh", nil
	}

	u, parseErr := url.Parse(rawURL)
	if parseErr != nil || u.Host != "github.com" {
		return "", "", "", fmt.Errorf("not a GitHub URL: %s", rawURL)
	}

	path := strings.TrimPrefix(u.Path, "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", fmt.Errorf("malformed GitHub URL path: %s", rawURL)
	}

	return parts[0], parts[1], "https", nil
}

// DetectMajorityProtocol returns the most common protocol from a list.
// Defaults to "ssh" on tie or empty input.
func DetectMajorityProtocol(protocols []string) string {
	if len(protocols) == 0 {
		return "ssh"
	}

	counts := make(map[string]int)
	for _, p := range protocols {
		counts[p]++
	}

	if counts["https"] > counts["ssh"] {
		return "https"
	}
	return "ssh"
}

// DeduplicateOrgs returns unique org names, case-insensitive but preserving first occurrence.
func DeduplicateOrgs(orgs []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, o := range orgs {
		key := strings.ToLower(o)
		if !seen[key] {
			seen[key] = true
			result = append(result, o)
		}
	}
	return result
}

// ScanDirectory walks a directory at depth 1 and identifies local git repos.
func ScanDirectory(ctx context.Context, dir string) ScanResult {
	var result ScanResult
	var protocols []string
	var rawOrgs []string

	entries, err := os.ReadDir(dir)
	if err != nil {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("cannot read directory %s: %v", dir, err))
		return result
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		gitDir := filepath.Join(path, ".git")

		info, statErr := os.Stat(gitDir)
		if statErr != nil || !info.IsDir() {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("%s: not a git repository, skipping", entry.Name()))
			continue
		}

		remoteURL, remoteErr := gitRemoteURL(ctx, path)
		if remoteErr != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("%s: cannot read remote URL: %v", entry.Name(), remoteErr))
			continue
		}

		org, _, proto, parseErr := ParseRemoteURL(remoteURL)
		if parseErr != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("%s: non-GitHub remote (%s), skipping", entry.Name(), remoteURL))
			continue
		}

		result.LocalRepos = append(result.LocalRepos, LocalRepo{
			Name:      entry.Name(),
			Path:      path,
			RemoteURL: remoteURL,
			Org:       org,
			Protocol:  proto,
		})
		rawOrgs = append(rawOrgs, org)
		protocols = append(protocols, proto)
	}

	result.Orgs = DeduplicateOrgs(rawOrgs)
	result.Protocol = DetectMajorityProtocol(protocols)

	return result
}

// gitRemoteURL runs git remote get-url origin in the given directory.
func gitRemoteURL(ctx context.Context, dir string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, remoteURLTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", "origin")
	cmd.Dir = dir
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("git remote get-url timed out after %s", remoteURLTimeout)
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ghRepoEntry matches the JSON shape from gh api /orgs/{org}/repos.
type ghRepoEntry struct {
	Name     string `json:"name"`
	CloneURL string `json:"clone_url"`
	SSHURL   string `json:"ssh_url"`
	Archived bool   `json:"archived"`
	Fork     bool   `json:"fork"`
}

// ParseGhRepoJSON parses the JSON output from gh api /orgs/{org}/repos.
func ParseGhRepoJSON(data []byte, org string) ([]RepoInfo, error) {
	var entries []ghRepoEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse GitHub API response: %w", err)
	}

	repos := make([]RepoInfo, 0, len(entries))
	for _, e := range entries {
		repos = append(repos, RepoInfo{
			Name:     e.Name,
			CloneURL: e.CloneURL,
			Org:      org,
			Archived: e.Archived,
			Fork:     e.Fork,
		})
	}
	return repos, nil
}

// FilterRepos filters repos based on archived and fork flags.
func FilterRepos(repos []RepoInfo, includeArchived, includeForks bool) []RepoInfo {
	result := make([]RepoInfo, 0, len(repos))
	for _, r := range repos {
		if !includeArchived && r.Archived {
			continue
		}
		if !includeForks && r.Fork {
			continue
		}
		result = append(result, r)
	}
	return result
}

// SelectCloneURL returns the appropriate clone URL based on the detected protocol.
func SelectCloneURL(repo RepoInfo, protocol, org string) string {
	if protocol == "ssh" {
		return fmt.Sprintf("git@github.com:%s/%s.git", org, repo.Name)
	}
	return repo.CloneURL
}

// ghEnv returns environment variables that suppress interactive prompts for gh CLI.
func ghEnv() []string {
	env := os.Environ()
	env = append(env, "GH_PROMPT_DISABLED=1")
	return env
}

// ListOrgRepos calls gh api to list all accessible repos for an org.
func ListOrgRepos(ctx context.Context, org string) ([]RepoInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, ghListTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "gh", "api", "--paginate",
		fmt.Sprintf("/orgs/%s/repos?per_page=100", org))
	cmd.Env = ghEnv()
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("gh api timed out for org %s after %s", org, ghListTimeout)
	}
	if err != nil {
		return nil, fmt.Errorf("gh api failed for org %s: %w", org, err)
	}
	return parsePaginatedJSON(out, org)
}

// parsePaginatedJSON handles gh --paginate output which concatenates JSON arrays.
// Uses json.Decoder to robustly handle adjacent arrays with or without whitespace.
func parsePaginatedJSON(data []byte, org string) ([]RepoInfo, error) {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "[]" {
		return nil, nil
	}

	var all []ghRepoEntry
	dec := json.NewDecoder(strings.NewReader(raw))
	for dec.More() {
		var page []ghRepoEntry
		if err := dec.Decode(&page); err != nil {
			return nil, fmt.Errorf("parse GitHub API response: %w", err)
		}
		all = append(all, page...)
	}

	repos := make([]RepoInfo, 0, len(all))
	for _, e := range all {
		repos = append(repos, RepoInfo{
			Name:     e.Name,
			CloneURL: e.CloneURL,
			Org:      org,
			Archived: e.Archived,
			Fork:     e.Fork,
		})
	}
	return repos, nil
}

// GhAvailable checks if the gh CLI is installed and authenticated.
func GhAvailable(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, ghAuthTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "gh", "auth", "status")
	cmd.Env = ghEnv()
	return cmd.Run() == nil
}

// ClassifyRepos compares remote repos against local repos and produces tasks.
func ClassifyRepos(local []LocalRepo, remote []RepoInfo, baseDir, protocol, org string) []ClassifiedTask {
	localByName := make(map[string]LocalRepo)
	for _, lr := range local {
		localByName[lr.Name] = lr
	}

	remoteByName := make(map[string]bool)
	var tasks []ClassifiedTask

	for _, rr := range remote {
		remoteByName[rr.Name] = true
		if lr, exists := localByName[rr.Name]; exists {
			tasks = append(tasks, ClassifiedTask{
				RepoName: rr.Name,
				Action:   "pull",
				LocalDir: lr.Path,
			})
		} else {
			tasks = append(tasks, ClassifiedTask{
				RepoName: rr.Name,
				Action:   "clone",
				CloneURL: SelectCloneURL(rr, protocol, org),
				LocalDir: filepath.Join(baseDir, rr.Name),
			})
		}
	}

	// Pull local repos not in the remote list (offline pull mode)
	for _, lr := range local {
		if !remoteByName[lr.Name] {
			tasks = append(tasks, ClassifiedTask{
				RepoName: lr.Name,
				Action:   "pull",
				LocalDir: lr.Path,
			})
		}
	}

	return tasks
}
