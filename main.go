package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

const (
	version            = "0.0.1"
	minGitMajorVersion = 2
	minGitMinorVersion = 38
)

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
		fmt.Fprintln(os.Stderr, `git-rebase-all: Rebase all branches across all worktrees.

Usage:
  Rebase onto a specified branch.
    git-rebase-all -b foo

  Rebase onto main if it exists, or master if that exists.
    git-rebse-all

  Print version information and exit
    git-rebase-all -v

Details:
  This program expects Git 2.38+ to be used.

  This program will update the target branch, collect all 'leaf' branches (that
  is, branches that are not reachable from any other branch), and rebase each
  leaf branch onto the (now-updated) target branch. The updates are performed
  with "git rebase --update-refs".

  See github.com/adamroyjones/git-rebase-all.`)
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
		return err
	}

	if exec.Command("git", "rev-parse", "--is-inside-work-tree").Run() != nil {
		return errors.New("the program is not being run from a Git directory")
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

	if err := s.decapitateAll(); err != nil {
		return fmt.Errorf("failed to detach the HEAD for each worktree: %w", err)
	}

	fmt.Printf("Updating %q...\n", s.targetBranch)
	if err := s.updateTargetBranch(); err != nil {
		return fmt.Errorf("updating target branch (%s): %w", s.targetBranch, err)
	}

	if err := s.constructLeaves(); err != nil {
		return fmt.Errorf("finding the leaf branches: %w", err)
	}

	fmt.Println("Updating leaf branches...")
	if err := s.updateBranches(); err != nil {
		return fmt.Errorf("updating worktree branches: %w", err)
	}

	return nil
}

func validateGitVersion() error {
	bs, err := exec.Command("git", "--version").CombinedOutput()
	s := strings.TrimSpace(string(bs))
	if err != nil {
		return fmt.Errorf("running `git --version`: %w (output: %s)", err, s)
	}
	_, version, ok := strings.Cut(s, "git version ")
	if !ok {
		return fmt.Errorf(`unexpected version string (expected: "git version <version>", given: %q)`, s)
	}

	majorStr, remainder, ok := strings.Cut(version, ".")
	if !ok {
		return fmt.Errorf(`unexpected version string (expected: "<major>.<minor>.<patch>", given: %q)`, version)
	}
	major, err := strconv.Atoi(majorStr)
	if err != nil {
		return fmt.Errorf("failed to parse the major version %q as an integer: %w", majorStr, err)
	}
	if major < minGitMajorVersion {
		return fmt.Errorf("the major version of `git` is too low (given: %d, minimum: %d)", major, minGitMajorVersion)
	}

	minorStr, _, ok := strings.Cut(remainder, ".")
	if !ok {
		return fmt.Errorf(`unexpected version string (expected: "<minor>.<minor>.<patch>", given: %q)`, version)
	}
	minor, err := strconv.Atoi(minorStr)
	if err != nil {
		return fmt.Errorf("failed to parse the minor version %q as an integer: %w", minorStr, err)
	}
	if minor < minGitMinorVersion {
		return fmt.Errorf("the minor version of `git` is too low (given: %d, minimum: %d)", minor, minGitMinorVersion)
	}

	return nil
}

func newState(targetBranch string) (*state, error) {
	currentDirectory, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("fetching the current directory: %w", err)
	}

	worktrees, err := worktrees()
	if err != nil {
		return nil, fmt.Errorf("fetching and parsing worktrees: %w", err)
	}

	branches, err := branches(currentDirectory)
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
		return nil, errors.New("no branch was specified and unable to find master or main")
	}

	currentBranch, err := currentBranch(currentDirectory)
	if err != nil {
		return nil, fmt.Errorf("finding current branch: %w", err)
	}

	return &state{
		worktrees:     worktrees,
		branches:      branches,
		currentDir:    currentDirectory,
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
			return fmt.Errorf("failed to the detach the HEAD (worktree directory: %s): %w", w.dir, err)
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

	// We remove the target branch from consideration; we update this independently.
	s.leaves = slices.DeleteFunc(leaves, func(str string) bool { return str == s.targetBranch })
	return nil
}

func (s *state) updateTargetBranch() error {
	if err := checkout(s.currentDir, s.targetBranch); err != nil {
		return fmt.Errorf("checking out the target branch (dir: %s, branch: %s): %w", s.currentDir, s.targetBranch, err)
	}
	if err := pull(s.currentDir); err != nil {
		return fmt.Errorf("pulling from %s: %w", s.currentDir, err)
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
			return fmt.Errorf("restoring the worktree (directory: %s, branch: %s): checking out: %w", w.dir, w.branch, err)
		}
	}
	return nil
}
