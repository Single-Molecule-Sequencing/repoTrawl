# Changelog

All notable changes to repoTrawl are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/), and the project adheres to
semantic versioning.

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
