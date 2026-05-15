package discover

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseRemoteURL(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		wantOrg   string
		wantRepo  string
		wantProto string
		wantErr   bool
	}{
		{
			name:      "SSH with .git suffix",
			url:       "git@github.com:Single-Molecule-Sequencing/smaseq.git",
			wantOrg:   "Single-Molecule-Sequencing",
			wantRepo:  "smaseq",
			wantProto: "ssh",
		},
		{
			name:      "SSH without .git suffix",
			url:       "git@github.com:Single-Molecule-Sequencing/smaseq",
			wantOrg:   "Single-Molecule-Sequencing",
			wantRepo:  "smaseq",
			wantProto: "ssh",
		},
		{
			name:      "HTTPS with .git suffix",
			url:       "https://github.com/Single-Molecule-Sequencing/smaseq.git",
			wantOrg:   "Single-Molecule-Sequencing",
			wantRepo:  "smaseq",
			wantProto: "https",
		},
		{
			name:      "HTTPS without .git suffix",
			url:       "https://github.com/Single-Molecule-Sequencing/smaseq",
			wantOrg:   "Single-Molecule-Sequencing",
			wantRepo:  "smaseq",
			wantProto: "https",
		},
		{
			name:    "non-GitHub URL",
			url:     "https://gitlab.com/org/repo.git",
			wantErr: true,
		},
		{
			name:    "malformed URL",
			url:     "not-a-url",
			wantErr: true,
		},
		{
			name:    "empty URL",
			url:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			org, repo, proto, err := ParseRemoteURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if org != tt.wantOrg {
				t.Errorf("org = %q, want %q", org, tt.wantOrg)
			}
			if repo != tt.wantRepo {
				t.Errorf("repo = %q, want %q", repo, tt.wantRepo)
			}
			if proto != tt.wantProto {
				t.Errorf("proto = %q, want %q", proto, tt.wantProto)
			}
		})
	}
}

func TestDetectMajorityProtocol(t *testing.T) {
	tests := []struct {
		name      string
		protocols []string
		want      string
	}{
		{"all SSH", []string{"ssh", "ssh", "ssh"}, "ssh"},
		{"all HTTPS", []string{"https", "https"}, "https"},
		{"majority SSH", []string{"ssh", "ssh", "https"}, "ssh"},
		{"majority HTTPS", []string{"https", "https", "ssh"}, "https"},
		{"empty", []string{}, "ssh"},
		{"tie favors ssh", []string{"ssh", "https"}, "ssh"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectMajorityProtocol(tt.protocols)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDeduplicateOrgs(t *testing.T) {
	input := []string{"OrgA", "orgA", "OrgB", "OrgA", "OrgC"}
	got := DeduplicateOrgs(input)

	if len(got) != 3 {
		t.Errorf("expected 3 unique orgs, got %d: %v", len(got), got)
	}
}

func TestScanDirectory(t *testing.T) {
	tmp := t.TempDir()

	repo1 := filepath.Join(tmp, "repo-a")
	os.MkdirAll(repo1, 0o755)
	gitRun(t, repo1, "git", "init")
	gitRun(t, repo1, "git", "remote", "add", "origin", "git@github.com:TestOrg/repo-a.git")

	repo2 := filepath.Join(tmp, "repo-b")
	os.MkdirAll(repo2, 0o755)
	gitRun(t, repo2, "git", "init")
	gitRun(t, repo2, "git", "remote", "add", "origin", "https://github.com/TestOrg/repo-b.git")

	os.MkdirAll(filepath.Join(tmp, "not-a-repo"), 0o755)

	os.WriteFile(filepath.Join(tmp, "somefile.txt"), []byte("hi"), 0o644)

	result := ScanDirectory(context.Background(), tmp)

	if len(result.LocalRepos) != 2 {
		t.Errorf("expected 2 local repos, got %d", len(result.LocalRepos))
	}
	if len(result.Orgs) != 1 {
		t.Errorf("expected 1 org, got %d: %v", len(result.Orgs), result.Orgs)
	}
	if len(result.Orgs) > 0 && result.Orgs[0] != "TestOrg" {
		t.Errorf("expected org TestOrg, got %s", result.Orgs[0])
	}
	if len(result.Warnings) != 1 {
		t.Errorf("expected 1 warning (not-a-repo), got %d: %v", len(result.Warnings), result.Warnings)
	}
}

func TestParseGhRepoJSON(t *testing.T) {
	jsonData := `[
		{"name": "repo-a", "clone_url": "https://github.com/Org/repo-a.git", "ssh_url": "git@github.com:Org/repo-a.git", "archived": false, "fork": false},
		{"name": "repo-b", "clone_url": "https://github.com/Org/repo-b.git", "ssh_url": "git@github.com:Org/repo-b.git", "archived": true, "fork": false},
		{"name": "repo-c", "clone_url": "https://github.com/Org/repo-c.git", "ssh_url": "git@github.com:Org/repo-c.git", "archived": false, "fork": true}
	]`

	repos, err := ParseGhRepoJSON([]byte(jsonData), "Org")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repos) != 3 {
		t.Fatalf("expected 3 repos, got %d", len(repos))
	}
	if repos[0].Name != "repo-a" || repos[0].Archived || repos[0].Fork {
		t.Errorf("repo-a: unexpected values: %+v", repos[0])
	}
	if !repos[1].Archived {
		t.Errorf("repo-b should be archived")
	}
	if !repos[2].Fork {
		t.Errorf("repo-c should be a fork")
	}
}

func TestFilterRepos(t *testing.T) {
	repos := []RepoInfo{
		{Name: "normal", Archived: false, Fork: false},
		{Name: "archived", Archived: true, Fork: false},
		{Name: "forked", Archived: false, Fork: true},
		{Name: "both", Archived: true, Fork: true},
	}

	got := FilterRepos(repos, true, false)
	if len(got) != 2 {
		t.Errorf("include archived, exclude forks: expected 2, got %d", len(got))
	}

	got = FilterRepos(repos, false, true)
	if len(got) != 2 {
		t.Errorf("exclude archived, include forks: expected 2, got %d", len(got))
	}

	got = FilterRepos(repos, true, true)
	if len(got) != 4 {
		t.Errorf("include all: expected 4, got %d", len(got))
	}

	got = FilterRepos(repos, false, false)
	if len(got) != 1 {
		t.Errorf("exclude all: expected 1, got %d", len(got))
	}
}

func TestSelectCloneURL(t *testing.T) {
	repo := RepoInfo{
		Name:     "repo",
		CloneURL: "https://github.com/Org/repo.git",
	}

	got := SelectCloneURL(repo, "ssh", "Org")
	if got != "git@github.com:Org/repo.git" {
		t.Errorf("expected SSH URL, got %q", got)
	}

	got = SelectCloneURL(repo, "https", "Org")
	if got != "https://github.com/Org/repo.git" {
		t.Errorf("expected HTTPS URL, got %q", got)
	}
}

func TestClassifyRepos(t *testing.T) {
	tmp := t.TempDir()

	existing := filepath.Join(tmp, "existing-repo")
	os.MkdirAll(existing, 0o755)
	gitRun(t, existing, "git", "init")
	gitRun(t, existing, "git", "remote", "add", "origin", "git@github.com:Org/existing-repo.git")

	localRepos := []LocalRepo{
		{Name: "existing-repo", Path: existing, Org: "Org"},
	}

	remoteRepos := []RepoInfo{
		{Name: "existing-repo", CloneURL: "https://github.com/Org/existing-repo.git", Org: "Org"},
		{Name: "new-repo", CloneURL: "https://github.com/Org/new-repo.git", Org: "Org"},
	}

	tasks := ClassifyRepos(localRepos, remoteRepos, tmp, "ssh", "Org")

	var pullCount, cloneCount int
	for _, task := range tasks {
		switch task.Action {
		case "pull":
			pullCount++
		case "clone":
			cloneCount++
		}
	}

	if pullCount != 1 {
		t.Errorf("expected 1 pull task, got %d", pullCount)
	}
	if cloneCount != 1 {
		t.Errorf("expected 1 clone task, got %d", cloneCount)
	}
}

func gitRun(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("command %s %v failed: %s\n%s", name, args, err, out)
	}
}

func TestParsePaginatedJSON(t *testing.T) {
	entry := func(name string) string {
		return `{"name":"` + name + `","clone_url":"https://github.com/Org/` + name + `.git","ssh_url":"git@github.com:Org/` + name + `.git","archived":false,"fork":false}`
	}

	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{"single array", `[` + entry("a") + `]`, 1, false},
		{"concatenated", `[` + entry("a") + `][` + entry("b") + `]`, 2, false},
		{"newline separated", `[` + entry("a") + "]" + "\n" + `[` + entry("b") + `]`, 2, false},
		{"whitespace separated", `[` + entry("a") + "]" + "  \n  " + `[` + entry("b") + `]`, 2, false},
		{"three pages", `[` + entry("a") + `][` + entry("b") + `][` + entry("c") + `]`, 3, false},
		{"empty string", "", 0, false},
		{"empty array", "[]", 0, false},
		{"invalid json", "{not json}", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repos, err := parsePaginatedJSON([]byte(tt.input), "Org")
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(repos) != tt.want {
				t.Errorf("got %d repos, want %d", len(repos), tt.want)
			}
		})
	}
}
