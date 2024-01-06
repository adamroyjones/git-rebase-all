package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"slices"
)

type worktree struct{ dir, branch string }

type state struct {
	worktrees     []worktree
	branches      []string
	currentDir    string
	currentBranch string
	targetBranch  string
}

// TODO: Handle the graph of branches. In particular, if b -> a -> master, make sure that a is rebased onto master and then b is rebased onto a.
// TODO: More gracefully handle errors: abort the rebase and revert the branch?
func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Fatal error: %v.\n", err)
		os.Exit(1)
	}
}

func run() (err error) {
	var targetBranch string
	flag.StringVar(&targetBranch, "b", "", "The branch onto which to rebase; defaults first to main, then to master, if unspecified.")
	flag.Parse()

	if exec.Command("git", "rev-parse", "--is-inside-work-tree").Run() != nil {
		return errors.New("the program is not being run from a Git directory")
	}

	s, err := newState(targetBranch)
	if err != nil {
		return fmt.Errorf("constructing state struct: %w", err)
	}

	fmt.Printf("Fetching, pruning, and updating '%s'...\n", s.targetBranch)
	if err := fetch(s.currentDir); err != nil {
		return fmt.Errorf("fetching and pruning: %w", err)
	}
	defer func() { err = errors.Join(err, s.restore()) }()

	if err := s.detachAllHEADS(); err != nil {
		return fmt.Errorf("failed to detach the HEAD for each worktree: %w", err)
	}

	// TODO: Build a graph of branches.
	for _, branch := range s.branches {
		children, err := branchChildren(s.currentDir, branch)
		if err != nil {
			return err
		}
		for _, child := range children {
			fmt.Printf("%s -> %s\n", branch, child)
		}
	}

	if err := s.updateTargetBranch(); err != nil {
		return fmt.Errorf("updating target branch (%s): %w", s.targetBranch, err)
	}

	fmt.Println("Updating branches...")
	if err := s.updateBranches(); err != nil {
		return fmt.Errorf("updating worktree branches: %w", err)
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

func (s *state) detachAllHEADS() error {
	for _, w := range s.worktrees {
		if err := detachHEAD(w.dir); err != nil {
			return fmt.Errorf("failed to the detach the HEAD (worktree directory: %s): %w", w.dir, err)
		}
	}
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

// TODO: Make use the graph here once you've constructed it properly.
func (s *state) updateBranches() error {
	for i, b := range s.branches {
		fmt.Printf("%s [%d/%d]...\n", b, i+1, len(s.branches))
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
