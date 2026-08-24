package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A TREE-WIDE ban on `gh auth status`, not just a pin on the two call sites
// that were once wrong.
//
// The lab has banned branching on `gh auth status` since 2026-07-28
// (lab-system/docs/failures/2026-07-28-stale-gh-slot-disabled-pr-triage.md) and
// enforces it with lab-system/scripts/check_gh_probes_the_active_account.py.
// That guard is scoped to (".sh", ".ps1") on purpose, and lab-system holds no
// Go at all, so widening its suffix list would only teach it to scan a tree
// that cannot contain the defect. Go was therefore unguarded, and this repo
// carried the call in TWO places until v0.4.0, one of them a package-level
// initializer that ran before main() and could wedge the whole binary.
// See lab-system/docs/failures/2026-08-24-a-startup-probe-that-ran-before-main.md
//
// Why the command is forbidden here rather than merely bounded: `gh auth
// status` resolves the ACTIVE account from hosts.yml but then reports on every
// OTHER configured account, falling through to the system secret service for
// any whose token is not stored in plaintext. On a headless host with a locked
// keyring and no prompter it waits on an unlock dialog nobody can answer.
// `gh api user`, `gh api rate_limit`, `gh auth token` and
// `gh config get git_protocol` all answer the same questions without touching
// the bus, measured at 0 D-Bus connects each.

// probeArgs matches the argument sequence, in either shape repoTrawl has used:
//
//	exec.Command("gh", "auth", "status")
//	someArgs = []string{"auth", "status"}
//
// It keys on the adjacent "auth","status" literals rather than on "gh", so a
// variadic args slice built away from the command name is still caught.
var probeArgs = regexp.MustCompile(`"auth"\s*,\s*"status"`)

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found above the working directory")
		}
		dir = parent
	}
}

// goSources returns every non-test .go file in the module.
//
// _test.go is skipped deliberately, and it is the only exemption: the guards
// for this rule have to be able to NAME the banned command in an assertion
// string, and this file does so itself. Skipping tests is what keeps the
// detector from finding itself, which is the classic way a grep-style check
// reports a finding that is only its own constant.
func goSources(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "vendor", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestNoGoSourceShellsOutToGhAuthStatus(t *testing.T) {
	root := moduleRoot(t)
	files := goSources(t, root)

	// Positive control: a probe that cannot find the thing when it IS there is
	// worth nothing, and a zero from it is not evidence. Prove the matcher
	// fires before trusting that it found nothing. The needle is assembled at
	// runtime so this file does not contain the literal sequence it bans.
	needle := `exec.Command("gh", ` + `"auth"` + `, ` + `"status"` + `)`
	if !probeArgs.MatchString(needle) {
		t.Fatal("the detector cannot match a known-bad line, so a clean result would prove nothing")
	}
	if len(files) == 0 {
		t.Fatal("walked the module and found no non-test Go files, so this check scanned nothing")
	}

	var findings []string
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		for n, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if probeArgs.MatchString(line) {
				rel, relErr := filepath.Rel(root, path)
				if relErr != nil {
					rel = path
				}
				findings = append(findings, rel+":"+itoa(n+1)+": "+strings.TrimSpace(line))
			}
		}
	}

	if len(findings) > 0 {
		t.Fatalf("Go source shells out to `gh auth status`, which can block forever on a locked\n"+
			"system keyring and took down every repoTrawl invocation before v0.4.0.\n"+
			"Use `gh api user`, `gh api rate_limit`, or `gh config get git_protocol` instead.\n  %s",
			strings.Join(findings, "\n  "))
	}

	t.Logf("scanned %d non-test Go files, no `gh auth status` invocation", len(files))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
