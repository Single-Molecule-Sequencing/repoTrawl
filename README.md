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
- **Archive markers**: writes a conspicuous local `ARCHIVED.md` + `.archived` into
  archived clones (from GitHub's `archived` flag) so humans and LLM agents can tell
  at a glance that a repo is stale — see [Archive markers](#archive-markers)
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

# Disable writing local ARCHIVED.md/.archived markers (on by default)
repotrawl --archive-markers=false
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
| `--archive-markers`  | `true`            | Write local `ARCHIVED.md`/`.archived` markers for archived repos |
| `-V`, `--version`    |                   | Print version and exit                 |

## Behavior

### Archive markers

The `archived` state of a repository lives in GitHub metadata, which is invisible
to anyone — human or LLM agent — working from a local clone. On every run,
repoTrawl makes it visible: for each archived repository that exists locally it
writes two **untracked** files into the clone root:

- `ARCHIVED.md` — an emoji-free banner that sorts to the top of a file listing and
  reads `ARCHIVED — DO NOT USE`, with `last_activity` and the marker version.
- `.archived` — a minimal `key=value` file (`archived=true`, `last_activity=…`)
  for cheap programmatic checks (`test -f .archived`).

The markers are added to `.git/info/exclude`, so they never dirty the working tree
and never need to be committed (archived repos are read-only on GitHub anyway).
They are not propagated through git — they propagate through the **binary**: every
member who runs repoTrawl regenerates them locally from the same authoritative
GitHub flag. The reconcile is **self-healing**: un-archiving a repository removes
its markers on the next run. Disable with `--archive-markers=false`.

This works independently of `--include-archived`: an already-cloned archived repo
is marked even when `--include-archived=false` keeps it out of the sync set. With
`--include-archived=false`, an already-cloned archived repo is also excluded from
pulls (it is no longer re-added by the offline-pull fallback) — pair the two to
both drop archived repos from the sync *and* flag any that already exist locally.

**Safety.** repoTrawl only writes, refreshes, or removes files carrying its own
sentinel. A pre-existing `ARCHIVED.md`/`.archived` it did not generate is never
overwritten or deleted — that repo is reported as `skipped` instead. The
`.git/info/exclude` entries live in a delimited, newline-safe managed block that is
removed cleanly when a repo is un-archived, and worktree/submodule clones (whose
`.git` is a file, not a directory) are skipped because their excludes cannot be
managed safely.

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
| `git remote get-url`           | 5 seconds  |
| `gh auth status`               | 10 seconds |
| `git status`                   | 15 seconds |
| `gh api` (repo listing)        | 60 seconds |
| `git pull` + submodule update  | 2 minutes  |
| `git clone` + submodule update | 10 minutes |

## Exit codes

| Code | Meaning                                   |
| ---- | ----------------------------------------- |
| `0`  | All operations succeeded (includes skips) |
| `1`  | One or more operations failed             |
| `2`  | Invalid arguments or configuration error  |
| `3`  | Completed with warnings (partial results) |

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
├── main_test.go                 # CLI flag preprocessing tests
├── internal/
│   ├── discover/
│   │   ├── types.go             # RepoInfo, LocalRepo, ScanResult, ClassifiedTask
│   │   ├── discover.go          # URL parsing, dir scanning, GitHub API, classification
│   │   └── discover_test.go
│   ├── sync/
│   │   ├── types.go             # Action, Status enums, Task, Result
│   │   ├── sync.go              # Worker pool, git pull/clone, timeout handling
│   │   └── sync_test.go
│   ├── marker/
│   │   ├── marker.go            # Local ARCHIVED.md/.archived reconcile from the archived flag
│   │   └── marker_test.go
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
