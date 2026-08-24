# Changelog

All notable changes to repoTrawl are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/), and the project adheres to
semantic versioning.

## [0.4.0] - 2026-08-24

### Fixed
- **A startup probe with no timeout could wedge the whole binary.** `internal/sync`
  detected the git protocol from a PACKAGE-LEVEL initializer, so it ran before
  `main()`, before flag parsing, and before `-V` could print a version and return.
  It shelled out to `gh auth status` with no context and no timeout at all, so
  anything that made that command slow made every repoTrawl invocation slow, and
  anything that made it block made every invocation block. On the Athey Lab hub on
  2026-08-24 that meant `repoupdater -V` never returned: not a sync, not a flag,
  just the version. It now asks `gh config get git_protocol`, which reads the same
  answer out of the config files, never consults the system keyring, and returned
  in 0.04s on the host where `gh auth status` returned never. Bounded by a 3s
  context regardless.

- **`gh auth status` is no longer used to decide whether gh is usable.**
  `discover.GhAvailable` probed with it under a 10s timeout, and `gh auth status`
  is the one gh subcommand that reaches into the system secret service: it can
  fall through to the keyring for any account whose token is not sitting in
  plaintext in `hosts.yml`. On a headless host with a locked keyring and no
  prompter, that call waits on an unlock dialog nobody can answer. The 10s
  deadline then fired and repoTrawl reported "gh CLI is not available or not
  authenticated" on a host where `gh api user` answered in 0.27s with the same
  token. The probe is now `gh api user`, measured at 0 D-Bus connects versus 1 for
  `auth status`, and it has the side benefit of proving the token actually WORKS
  rather than merely that one is stored.

### Added
- **`discover.GhProbe`** returns the REASON the gh probe failed, so callers can
  tell "gh is missing" from "gh is signed out" from "gh never answered".
  `--org` failures now report that reason instead of the single blanket sentence
  "not available or not authenticated", which named three different failures at
  once and named the wrong one.

### Notes
- Both probes set `cmd.WaitDelay`. This is load-bearing, not defensive style:
  `exec.CommandContext` kills only the direct child while `CombinedOutput` waits
  for the output pipes to reach EOF, so a surviving grandchild can hold a probe
  open long past its deadline. A timeout fixture caught exactly that in the first
  draft of this fix, where a "fixed" probe still sat for 30s against a 250ms
  deadline.
- Every package's test binary ran that startup initializer too, so the suite was
  paying the same cost: `go test ./...` went from roughly 362s to roughly 9s.

## [0.3.1] - 2026-06-18

### Changed
- **Empty and missing-upstream repos are skipped, not failed.** A `git pull` that
  fails only because the tracked upstream branch does not exist on the remote — an
  empty repository with no default branch, or a branch deleted/renamed upstream —
  is now reported as `⚠ no upstream branch` (skipped) instead of `✗ failed`. This
  matches repoTrawl's safe-by-default philosophy (dirty and diverged repos already
  skip) and keeps the exit code at success when the only "failures" are repos
  there is genuinely nothing to pull from. Pull-error classification moved into a
  unit-tested `classifyPullError` helper. New `StatusSkippedNoUpstream` outcome.

## [0.3.0] - 2026-06-06

### Added
- **Local archive markers.** repoTrawl now makes GitHub's authoritative `archived`
  flag visible on the filesystem. For every archived repository that exists
  locally it writes two untracked marker files into the clone root:
  - `ARCHIVED.md` — a conspicuous, emoji-free banner (sorts to the top of a file
    listing) that humans and LLM agents see immediately.
  - `.archived` — a minimal `key=value` file (`archived=true`, `last_activity=…`)
    for cheap programmatic checks.

  The markers are **local and untracked** — added to `.git/info/exclude` so they
  never dirty the working tree and never need to be committed (archived repos are
  read-only on GitHub). They propagate across the lab via the binary, not git:
  every member who runs repoTrawl regenerates them locally from the same
  authoritative GitHub flag. The reconcile is **self-healing** — un-archiving a
  repository removes its markers on the next run.

  Disable with `--archive-markers=false`. Works whether or not archived repos are
  in the sync set, so already-cloned archived repos are marked even under
  `--include-archived=false`.

  **Safe by construction.** repoTrawl only ever writes, refreshes, or removes files
  that carry its own sentinel. A pre-existing `ARCHIVED.md`/`.archived` it did not
  generate is never overwritten or deleted (the repo is reported as `skipped`). The
  `.git/info/exclude` entries live in a delimited, newline-safe managed block that
  is removed cleanly on un-archive, and worktree/submodule clones (whose `.git` is a
  file) are skipped because their excludes cannot be managed safely.
- `pushed_at` is now captured from the GitHub API and recorded as the marker's
  `last_activity` date.

### Fixed
- `--include-archived=false` now also excludes **already-cloned** archived (and
  forked) repositories from the sync. Previously the offline-pull fallback re-added
  any local repo missing from the filtered remote list, so an archived repo that was
  already on disk kept getting pulled. Genuinely offline/personal clones (absent
  from the org entirely) are still pulled.

### Notes
- This release does not change the default `--include-archived=true`. Pair the
  markers with `--include-archived=false` if you also want archived repos excluded
  from the sync entirely (the lab bootstrap can set this at its call site).

## [0.2.0]

- Parallel pull/clone pool, org auto-detection, verbosity levels, dry-run.

## [0.1.0]

- Initial release.
