package main

import (
	"cmp"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
)

const version = "0.0.6"

const minGitMajorVersion, minGitMinorVersion = 2, 38

type worktree struct{ dir, branch string }

type state struct {
	worktrees []worktree
	// branch -> commit SHA
	branches         map[string]string
	branchesToRebase []string
	currentDir       string
	targetBranch     string
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stdout, `git-rebase-all: Rebase all branches across all worktrees.

Usage:
  Rebase onto a specified branch.
    git-rebase-all -b foo

  Rebase onto main if it exists, else master if it exists, and otherwise error.
    git-rebase-all

  Print version information and exit
    git-rebase-all -v

Details:
  This program requires Git %d.%d+.

  This program will update the target branch, collect all 'leaf' branches (that
  is, branches that are not reachable from any other branch), and rebase each
  leaf branch onto the (now-updated) target branch. The updates are performed
  with "git rebase --update-refs".

  See github.com/adamroyjones/git-rebase-all.
`, minGitMajorVersion, minGitMinorVersion)
	}

	var targetBranch string
	var v bool
	flag.BoolVar(&v, "v", false, "Print version information and exit.")
	flag.StringVar(&targetBranch, "b", "", "The branch onto which to rebase; defaults first to main, then to master, if unspecified.")
	flag.Parse()

	if v {
		fmt.Println("git-rebase-all " + version)
		os.Exit(0)
	}

	if err := run(targetBranch); err != nil {
		fmt.Fprintf(os.Stderr, "Fatal error: %v.\n", err)
		os.Exit(1)
	}
}

func run(targetBranch string) (err error) {
	if err := validateGitVersion(); err != nil {
		return fmt.Errorf("validating the version of git :%w", err)
	}

	if bs, err := exec.Command("git", "rev-parse", "--is-inside-work-tree").CombinedOutput(); err != nil {
		return fmt.Errorf("checking whether the program is being run from a git directory: %w (output: %s)", err, trimbs(bs))
	}

	s, err := newState(targetBranch)
	if err != nil {
		return fmt.Errorf("constructing state struct: %w", err)
	}

	if err := s.errIfUncommittedChanges(); err != nil {
		return fmt.Errorf("verifying that there are no uncommitted changes: %w", err)
	}

	fmt.Println("Fetching and pruning...")
	if err := fetch(s.currentDir); err != nil {
		return fmt.Errorf("fetching and pruning: %w", err)
	}
	defer func() { err = errors.Join(err, s.restore()) }()

	// git doesn't permit a branch to be checked out in more than one worktree. By
	// decapitating each worktree, we can work in a single directory (namely, the
	// current directory).
	if err := s.decapitateAll(); err != nil {
		return fmt.Errorf("failed to detach the HEAD for each worktree: %w", err)
	}

	fmt.Printf("Updating %q...\n", s.targetBranch)
	if err := s.updateTargetBranch(); err != nil {
		return fmt.Errorf("updating target branch (%s): %w", s.targetBranch, err)
	}

	fmt.Println("Updating the branches...")
	if err := s.constructBranchesToRebase(); err != nil {
		return fmt.Errorf("constructing the list of branches to rebase: %w", err)
	}

	if err := s.rebaseBranches(); err != nil {
		return fmt.Errorf("rebasing the branches: %w", err)
	}

	return nil
}

func validateGitVersion() error {
	bs, err := exec.Command("git", "--version").CombinedOutput()
	s := trimbs(bs)
	if err != nil {
		return fmt.Errorf("running `git --version`: %w (output: %s)", err, s)
	}

	var major, minor, patch int
	if _, err := fmt.Sscanf(s, "git version %d.%d.%d", &major, &minor, &patch); err != nil {
		return fmt.Errorf(`expected a version string in the form "git version <major>.<minor>.<patch>"; given %q`, s)
	}
	if major < minGitMajorVersion {
		return fmt.Errorf("the major version of git is too low (given: %d, minimum: %d)", major, minGitMajorVersion)
	}
	if major == minGitMajorVersion && minor < minGitMinorVersion {
		return fmt.Errorf("the minor version of git is too low (given: %d, minimum: %d)", minor, minGitMinorVersion)
	}
	return nil
}

func newState(targetBranch string) (*state, error) {
	currentDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("fetching the current directory: %w", err)
	}

	worktrees, err := worktrees()
	if err != nil {
		return nil, fmt.Errorf("fetching and parsing worktrees: %w", err)
	}

	branches, err := branches(currentDir)
	if err != nil {
		return nil, fmt.Errorf("listing the local branches: %w", err)
	}

	branchNames := sortedKeys(branches)
	if targetBranch != "" && !contains(branchNames, targetBranch) {
		return nil, fmt.Errorf("the specified branch %q could not be found", targetBranch)
	}
	if targetBranch == "" && contains(branchNames, "main") {
		targetBranch = "main"
	}
	if targetBranch == "" && contains(branchNames, "master") {
		targetBranch = "master"
	}
	if targetBranch == "" {
		return nil, errors.New("no branch was specified and main and master could not be found")
	}

	return &state{
		worktrees:    worktrees,
		branches:     branches,
		currentDir:   currentDir,
		targetBranch: targetBranch,
	}, nil
}

func (s *state) errIfUncommittedChanges() error {
	for _, w := range s.worktrees {
		out, err := status(w.dir)
		if err != nil {
			return fmt.Errorf("checking for uncommitted changes (dir: %s): %w", w.dir, err)
		}
		if len(out) > 0 {
			return fmt.Errorf("there are uncommitted changes (dir: %s)", w.dir)
		}
	}
	return nil
}

func (s *state) decapitateAll() error {
	for _, w := range s.worktrees {
		if err := decapitate(w.dir); err != nil {
			return fmt.Errorf("failed to the detach the HEAD (dir: %s): %w", w.dir, err)
		}
	}
	return nil
}

// constructBranchesToRebase comprises two types of branch: "leaf" branches and
// those branches that are "behind" the target branch and so can be
// fast-forwarded. We'll collapse any distinction between the two categories.
func (s *state) constructBranchesToRebase() error {
	branchesToRebase := sortedKeys(s.branches)
	targetSHA, ok := s.branches[s.targetBranch]
	if !ok {
		return fmt.Errorf("unable to find the branch %q in the state: this should be unreachable", s.targetBranch)
	}

	i := 0
	for _, branch := range branchesToRebase {
		children, err := s.branchChildren(s.currentDir, branch)
		if err != nil {
			return err
		}

		// If a branch has no children, it is a "leaf" branch.
		if len(children) == 0 {
			branchesToRebase[i] = branch
			i++
			continue
		}

		// If a branch has the target branch as a child, and if the branch and the
		// target branch don't point to the same commit, then we should rebase.
		if slices.Contains(children, s.targetBranch) {
			branchSHA, ok := s.branches[branch]
			if !ok {
				return fmt.Errorf("unable to find the branch %q in the state: this should be unreachable", branch)
			}
			if branchSHA != targetSHA {
				branchesToRebase[i] = branch
				i++
				continue
			}
		}
	}
	branchesToRebase = branchesToRebase[:i]

	// We remove the target branch from consideration as we update this independently.
	s.branchesToRebase = slices.DeleteFunc(branchesToRebase, func(b string) bool { return b == s.targetBranch })
	return nil
}

func (s *state) updateTargetBranch() error {
	if err := checkout(s.currentDir, s.targetBranch); err != nil {
		return fmt.Errorf("checking out the target branch (dir: %s, branch: %s): %w", s.currentDir, s.targetBranch, err)
	}
	if err := pull(s.currentDir); err != nil {
		return fmt.Errorf("pulling (dir: %s, branch: %s): %w", s.currentDir, s.targetBranch, err)
	}

	newSHA, err := branchToSHA(s.currentDir, s.targetBranch)
	if err != nil {
		return fmt.Errorf("updating the target branch (%s) commit SHA: %w", s.targetBranch, err)
	}
	s.branches[s.targetBranch] = newSHA
	return nil
}

func (s *state) rebaseBranches() error {
	for i, b := range s.branchesToRebase {
		fmt.Printf("  %s [%d/%d]...\n", b, i+1, len(s.branchesToRebase))
		if err := checkout(s.currentDir, b); err != nil {
			return fmt.Errorf("checking out a branch (dir: %s, branch: %s): %w", s.currentDir, b, err)
		}
		if err := rebase(s.currentDir, s.targetBranch); err != nil {
			return fmt.Errorf("rebasing %q onto %q (dir: %s): %w", b, s.targetBranch, s.currentDir, err)
		}
	}
	return nil
}

func (s *state) restore() error {
	for _, w := range s.worktrees {
		if err := checkout(w.dir, w.branch); err != nil {
			return fmt.Errorf("restoring the worktree (dir: %s, branch: %s): checking out: %w", w.dir, w.branch, err)
		}
	}
	return nil
}

func trimbs(bs []byte) string { return strings.TrimSpace(string(bs)) }

func sortedKeys[K cmp.Ordered, V any](m map[K]V) []K {
	ks := make([]K, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}

// contains presupposes that xs is sorted.
func contains[K cmp.Ordered](xs []K, x K) bool {
	_, ok := slices.BinarySearch(xs, x)
	return ok
}
