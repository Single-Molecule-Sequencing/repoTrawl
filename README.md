# repoTrawl

Parallel-sync every repository in a GitHub organization. Pulls existing repos, clones new ones, skips dirty working trees, all in seconds.

```txt
$ repotrawl --dir ~/repos -v
[1/92] lab-wiki ........ ✓ up to date
[2/92] smaseq .......... ✓ 3 files changed, 12 insertions(+), 4 deletions(-)
[3/92] portal .......... ⚠ uncommitted changes
[4/92] new-project ..... ✓ cloned
...

repotrawl — Single-Molecule-Sequencing (92 repos)

 REPO              ACTION  STATUS
 new-project       clone   ✓ cloned
 smaseq            pull    ✓ 3 files changed
 lab-wiki          pull    ✓ up to date
 portal            pull    ⚠ uncommitted changes

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
 ✓ 1 cloned  ✓ 89 pulled  ⚠ 2 skipped
 Completed in 6.8s
```

## Features

- **Parallel execution**: goroutine worker pool with configurable concurrency
- **Auto-discovery**: detects the GitHub org from local repo remotes
- **New repo cloning**: discovers repos via `gh` API, clones any missing locally
- **Safe by default**: skips dirty repos, uses `--ff-only`, never force-pushes
- **Submodule support**: runs `git submodule update --init --recursive` after pull/clone
- **Timeout protection**: per-operation deadlines prevent hanging on credential prompts
- **Cross-platform**: static binaries for macOS (arm64/amd64), Linux, and Windows

## Install

### GitHub Releases (recommended)

Download the latest binary from [Releases](https://github.com/Single-Molecule-Sequencing/repoTrawl/releases) and place it on your `PATH`.

### From source

```bash
go install github.com/Single-Molecule-Sequencing/repotrawl@latest
```

### Prerequisites

- **git**: required for all operations
- **[gh](https://cli.github.com/)**: required for new repo discovery (cloning). Without `gh`, repotrawl still pulls all local repos.

```bash
# Authenticate gh (one-time setup)
gh auth login
```

## Usage

```bash
# Sync all repos in current directory
repotrawl

# Sync repos in a specific directory
repotrawl --dir ~/Developer/repos

# Specify the org explicitly
repotrawl --org Single-Molecule-Sequencing

# Preview what would happen without executing
repotrawl --dry-run

# Streaming progress output
repotrawl -v

# Full git output (useful for debugging)
repotrawl -vv

# Limit concurrency
repotrawl --jobs 4

# Include forked repos (excluded by default)
repotrawl --include-forks

# Exclude archived repos (included by default)
repotrawl --include-archived=false
```

## Flags

| Flag                 | Default           | Description                            |
| -------------------- | ----------------- | -------------------------------------- |
| `--dir`              | `.`               | Directory containing repositories      |
| `--org`              | auto-detect       | GitHub organization name               |
| `--jobs`             | `min(NumCPU, 10)` | Maximum concurrent git operations      |
| `-v`                 | off               | Streaming progress (one line per repo) |
| `-vv`                | off               | Trace mode (full git output per repo)  |
| `--dry-run`          | `false`           | Show plan without executing            |
| `--include-archived` | `true`            | Include archived repositories          |
| `--include-forks`    | `false`           | Include forked repositories            |
| `-V`, `--version`    |                   | Print version and exit                 |

## Behavior

### Pull strategy

Uses `git pull --ff-only`. If the local branch has diverged from upstream, the repo is reported as `⚠ diverged` and left unchanged.

### Dirty repos

Repos with uncommitted changes (`git status --porcelain` non-empty) are skipped with `⚠ uncommitted changes`. No data is ever lost.

### Org detection

When `--org` is not specified, repotrawl scans the `origin` remote of each local repo to determine the GitHub org. The majority protocol (SSH vs HTTPS) is used for clone URLs.

### Offline / no `gh`

If `gh` is not installed or authenticated, repotrawl degrades gracefully to pull-only mode. It pulls all local repos but cannot discover or clone new remote repos.

### Timeouts

Each operation has a deadline to prevent hanging on SSH passphrase prompts or slow networks:

| Operation                      | Timeout    |
| ------------------------------ | ---------- |
| `git status`                   | 15 seconds |
| `git pull` + submodule update  | 2 minutes  |
| `git clone` + submodule update | 10 minutes |

## Exit codes

| Code | Meaning                                   |
| ---- | ----------------------------------------- |
| `0`  | All operations succeeded (includes skips) |
| `1`  | One or more operations failed             |
| `2`  | Invalid arguments or configuration error  |

## Development

```bash
# Build
go build -ldflags "-X main.version=dev" -o repotrawl .

# Test (with race detector)
go test ./... -v -race -count=1

# Test a specific package
go test ./internal/sync/ -v -run TestRunPool
```

### Project structure

```txt
.
├── main.go                      # CLI entry, flag parsing, orchestration
├── internal/
│   ├── discover/
│   │   ├── types.go             # RepoInfo, LocalRepo, ScanResult, ClassifiedTask
│   │   ├── discover.go          # URL parsing, dir scanning, GitHub API, classification
│   │   └── discover_test.go
│   ├── sync/
│   │   ├── types.go             # Action, Status enums, Task, Result
│   │   ├── sync.go              # Worker pool, git pull/clone, timeout handling
│   │   └── sync_test.go
│   └── output/
│       ├── types.go             # Verbosity, Config
│       ├── output.go            # Summary table, verbose/trace rendering
│       └── output_test.go
├── .goreleaser.yaml             # Cross-platform release config
└── .github/workflows/
    ├── ci.yml                   # Test matrix (ubuntu/macos/windows)
    └── release.yml              # goreleaser on tag push
```

## License

[MIT](LICENSE)
