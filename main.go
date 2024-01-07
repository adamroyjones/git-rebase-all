package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
)

const version = "0.0.1"

const minGitMajorVersion, minGitMinorVersion = 2, 38

type worktree struct{ dir, branch string }

type state struct {
	worktrees     []worktree
	branches      []string
	leaves        []string
	currentDir    string
	currentBranch string
	targetBranch  string
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

	if exec.Command("git", "rev-parse", "--is-inside-work-tree").Run() != nil {
		return errors.New("the program is not being run from a git directory")
	}

	s, err := newState(targetBranch)
	if err != nil {
		return fmt.Errorf("constructing state struct: %w", err)
	}

	if err := s.unstagedChanges(); err != nil {
		return fmt.Errorf("verifying that there are no unstaged changes: %w", err)
	}

	fmt.Println("Fetching and pruning...")
	if err := fetch(s.currentDir); err != nil {
		return fmt.Errorf("fetching and pruning: %w", err)
	}
	defer func() { err = errors.Join(err, s.restore()) }()

	// git doesn't permit a branch to be checked out in more than one worktree. By
	// decapitating each worktree, the program can then work in just one directory
	// (namely, the current directory).
	if err := s.decapitateAll(); err != nil {
		return fmt.Errorf("failed to detach the HEAD for each worktree: %w", err)
	}

	fmt.Printf("Updating %q...\n", s.targetBranch)
	if err := s.updateTargetBranch(); err != nil {
		return fmt.Errorf("updating target branch (%s): %w", s.targetBranch, err)
	}

	fmt.Println("Updating the leaf branches...")
	if err := s.constructLeaves(); err != nil {
		return fmt.Errorf("finding the leaf branches: %w", err)
	}

	if err := s.updateBranches(); err != nil {
		return fmt.Errorf("updating the leaf branches: %w", err)
	}

	return nil
}

func validateGitVersion() error {
	bs, err := exec.Command("git", "--version").CombinedOutput()
	s := strings.TrimSpace(string(bs))
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
		return nil, fmt.Errorf("finding current branches: %w", err)
	}

	if targetBranch != "" && !slices.Contains(branches, targetBranch) {
		return nil, fmt.Errorf("the specified branch %q could not be found", targetBranch)
	}
	if targetBranch == "" && slices.Contains(branches, "main") {
		targetBranch = "main"
	}
	if targetBranch == "" && slices.Contains(branches, "master") {
		targetBranch = "master"
	}
	if targetBranch == "" {
		return nil, errors.New("no branch was specified and main and master could not be found")
	}

	currentBranch, err := currentBranch(currentDir)
	if err != nil {
		return nil, fmt.Errorf("finding current branch: %w", err)
	}

	return &state{
		worktrees:     worktrees,
		branches:      branches,
		currentDir:    currentDir,
		currentBranch: currentBranch,
		targetBranch:  targetBranch,
	}, nil
}

func (s *state) unstagedChanges() error {
	for _, w := range s.worktrees {
		out, err := status(w.dir)
		if err != nil {
			return err
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

func (s *state) constructLeaves() error {
	leaves := make([]string, len(s.branches))
	copy(leaves, s.branches)

	for _, branch := range s.branches {
		children, err := branchChildren(s.currentDir, branch)
		if err != nil {
			return err
		}
		if len(children) > 0 {
			leaves = slices.DeleteFunc(leaves, func(str string) bool { return str == branch })
		}
	}

	// We remove the target branch from consideration as we update this independently.
	s.leaves = slices.DeleteFunc(leaves, func(str string) bool { return str == s.targetBranch })
	return nil
}

func (s *state) updateTargetBranch() error {
	if err := checkout(s.currentDir, s.targetBranch); err != nil {
		return fmt.Errorf("checking out the target branch (dir: %s, branch: %s): %w", s.currentDir, s.targetBranch, err)
	}
	if err := pull(s.currentDir); err != nil {
		return fmt.Errorf("pulling (dir: %s, branch: %s): %w", s.currentDir, s.targetBranch, err)
	}
	return nil
}

func (s *state) updateBranches() error {
	for i, b := range s.leaves {
		fmt.Printf("  %s [%d/%d]...\n", b, i+1, len(s.leaves))
		if err := checkout(s.currentDir, b); err != nil {
			return err
		}
		if err := rebase(s.currentDir, s.targetBranch); err != nil {
			return err
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
