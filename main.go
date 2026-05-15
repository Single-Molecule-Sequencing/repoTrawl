package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Single-Molecule-Sequencing/repotrawl/internal/discover"
	"github.com/Single-Molecule-Sequencing/repotrawl/internal/output"
	rpSync "github.com/Single-Molecule-Sequencing/repotrawl/internal/sync"
)

var version = "dev"

func main() {
	os.Exit(run())
}

func run() int {
	var (
		orgFlag         string
		dirFlag         string
		jobsFlag        int
		verboseCount    verboseFlag
		includeArchived bool
		includeForks    bool
		dryRun          bool
		showVersion     bool
	)

	flag.StringVar(&orgFlag, "org", "", "GitHub org name (default: auto-detect)")
	flag.StringVar(&dirFlag, "dir", ".", "Directory containing repos")
	flag.IntVar(&jobsFlag, "jobs", 0, "Max concurrent operations (default: auto)")
	flag.Var(&verboseCount, "v", "Increase verbosity (-v=streaming, -vv=trace)")
	flag.BoolVar(&includeArchived, "include-archived", true, "Include archived repos")
	flag.BoolVar(&includeForks, "include-forks", false, "Include forked repos")
	flag.BoolVar(&dryRun, "dry-run", false, "Show plan without executing")
	flag.BoolVar(&showVersion, "version", false, "Print version")
	flag.BoolVar(&showVersion, "V", false, "Print version (shorthand)")

	flag.Parse()

	if showVersion {
		fmt.Printf("repoTrawl %s\nHenry (Haoran) Li — Athey Lab @UMich\n", version)
		return 0
	}

	if jobsFlag <= 0 {
		jobsFlag = runtime.NumCPU()
		if jobsFlag > 10 {
			jobsFlag = 10
		}
	}

	verbosity := output.Verbosity(verboseCount)
	isTTY := isTerminal()

	dir, err := resolveDir(dirFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 2
	}

	// Scan local directory
	scanResult := discover.ScanDirectory(dir)
	for _, w := range scanResult.Warnings {
		if verbosity >= output.VerboseStreaming {
			fmt.Fprintf(os.Stderr, "warning: %s\n", w)
		}
	}

	// Determine org(s)
	orgs := scanResult.Orgs
	explicitOrg := orgFlag != ""
	if explicitOrg {
		orgs = []string{orgFlag}
	}

	// Discover remote repos
	var allTasks []rpSync.Task
	ghOK := discover.GhAvailable()

	if !ghOK && explicitOrg {
		fmt.Fprintf(os.Stderr, "error: --org specified but gh CLI is not available or not authenticated\n")
		fmt.Fprintf(os.Stderr, "Run: gh auth login\n")
		return 2
	}

	if ghOK && len(orgs) > 0 {
		for _, org := range orgs {
			remoteRepos, listErr := discover.ListOrgRepos(org)
			if listErr != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to list repos for %s: %v\n", org, listErr)
				continue
			}
			filtered := discover.FilterRepos(remoteRepos, includeArchived, includeForks)

			var orgLocalRepos []discover.LocalRepo
			for _, lr := range scanResult.LocalRepos {
				if strings.EqualFold(lr.Org, org) {
					orgLocalRepos = append(orgLocalRepos, lr)
				}
			}

			classified := discover.ClassifyRepos(orgLocalRepos, filtered, dir, scanResult.Protocol, org)
			for _, ct := range classified {
				allTasks = append(allTasks, rpSync.Task{
					RepoName: ct.RepoName,
					Action:   toSyncAction(ct.Action),
					CloneURL: ct.CloneURL,
					LocalDir: ct.LocalDir,
				})
			}
		}
	} else {
		if !ghOK && len(orgs) > 0 {
			fmt.Fprintf(os.Stderr, "warning: gh CLI unavailable — skipping new repo discovery\n")
		}
		if len(orgs) == 0 && len(scanResult.LocalRepos) > 0 {
			fmt.Fprintf(os.Stderr, "warning: could not detect org — pulling local repos only. Use --org to enable cloning.\n")
		}

		for _, lr := range scanResult.LocalRepos {
			allTasks = append(allTasks, rpSync.Task{
				RepoName: lr.Name,
				Action:   rpSync.ActionPull,
				LocalDir: lr.Path,
			})
		}
	}

	if len(allTasks) == 0 {
		fmt.Println("No repos found.")
		return 0
	}

	// Dry run
	if dryRun {
		fmt.Printf("Dry run — %d tasks:\n", len(allTasks))
		for _, t := range allTasks {
			fmt.Printf("  %s: %s\n", t.Action, t.RepoName)
		}
		return 0
	}

	// Execute
	orgName := "multiple orgs"
	if len(orgs) == 1 {
		orgName = orgs[0]
	}

	cfg := output.Config{
		Verbosity: verbosity,
		Color:     isTTY,
		OrgName:   orgName,
	}

	var progressFn rpSync.ProgressFunc
	if verbosity == output.VerboseStreaming {
		progressFn = func(idx, total int, r rpSync.Result) {
			output.RenderVerboseLine(os.Stdout, idx, total, r, isTTY)
		}
	} else if verbosity == output.VerboseTrace {
		progressFn = func(idx, total int, r rpSync.Result) {
			output.RenderTraceLine(os.Stdout, idx, total, r)
		}
	}

	start := time.Now()
	results := rpSync.RunPool(context.Background(), allTasks, jobsFlag, progressFn)
	elapsed := time.Since(start).Milliseconds()

	// Report
	if verbosity >= output.VerboseStreaming {
		fmt.Println()
	}
	output.RenderSummaryTable(os.Stdout, results, cfg, elapsed)

	for _, r := range results {
		if r.Status == rpSync.StatusFailed {
			return 1
		}
	}
	return 0
}

// verboseFlag implements flag.Value to support -v and -vv (counted occurrences).
type verboseFlag int

func (v *verboseFlag) String() string { return fmt.Sprint(int(*v)) }
func (v *verboseFlag) Set(string) error {
	*v++
	return nil
}
func (v *verboseFlag) IsBoolFlag() bool { return true }

func toSyncAction(action string) rpSync.Action {
	switch action {
	case "clone":
		return rpSync.ActionClone
	case "pull":
		return rpSync.ActionPull
	default:
		return rpSync.ActionSkip
	}
}

func resolveDir(dir string) (string, error) {
	if dir == "." {
		return os.Getwd()
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", dir)
	}
	return filepath.Abs(dir)
}

func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
