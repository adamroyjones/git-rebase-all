package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
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
	if exec.Command("git", "rev-parse", "--is-inside-work-tree").Run() != nil {
		return errors.New("the program is not being run from a Git directory")
	}

	s, err := newState()
	if err != nil {
		return fmt.Errorf("constructing state struct: %w", err)
	}
	defer func() { err = errors.Join(err, s.restore()) }()

	fmt.Printf("Fetching, pruning, and updating '%s'...\n", s.targetBranch)
	if err := fetch(); err != nil {
		return fmt.Errorf("fetching and pruning: %w", err)
	}

	if err := s.updateTargetBranch(); err != nil {
		return fmt.Errorf("updating target branch (%s): %w", s.targetBranch, err)
	}

	fmt.Println("Updating worktree branches...")
	if err := s.updateWorktreeBranches(); err != nil {
		return fmt.Errorf("updating worktree branches: %w", err)
	}

	fmt.Println("Updating non-worktree branches...")
	if err := s.updateNonworktreeBranches(); err != nil {
		return fmt.Errorf("updating non-worktree branches: %w", err)
	}

	return nil
}

func newState() (*state, error) {
	worktrees, err := worktrees()
	if err != nil {
		return nil, fmt.Errorf("fetching and parsing worktrees: %w", err)
	}

	branches, err := branches()
	if err != nil {
		return nil, fmt.Errorf("finding current branches: %w", err)
	}

	currentBranch, err := currentBranch()
	if err != nil {
		return nil, fmt.Errorf("finding current branch: %w", err)
	}

	currentDirectory, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("fetching current directory: %w", err)
	}

	if !slices.ContainsFunc(worktrees, func(w worktree) bool {
		// TODO: Make the directory check more robust.
		return strings.HasPrefix(currentDirectory, w.dir) && currentBranch == w.branch
	}) {
		return nil, fmt.Errorf("unable to find the current directory (%s) and branch (%s) amongst the worktrees (%+v)", currentDirectory, currentBranch, worktrees)
	}

	// TODO: Make this more robust. There's nothing special about these names.
	hasMaster := slices.Contains(branches, "master")
	hasMain := slices.Contains(branches, "main")
	if hasMaster && hasMain {
		return nil, errors.New("unexpected situation: the branch has both `master` and `main` branches")
	}
	if !hasMaster && !hasMain {
		return nil, errors.New("unexpected situation: the branch has neither `master` nor `main` branches")
	}

	s := state{
		worktrees:     worktrees,
		branches:      branches,
		currentDir:    currentDirectory,
		currentBranch: currentBranch,
	}

	if hasMain {
		s.targetBranch = "main"
	} else {
		s.targetBranch = "master"
	}

	return &s, nil
}

func (s *state) updateTargetBranch() error {
	targetBranchIsWorktree := false
	var targetWorktree worktree
	for _, w := range s.worktrees {
		if w.branch == s.targetBranch {
			targetBranchIsWorktree = true
			targetWorktree = w
			break
		}
	}

	if !targetBranchIsWorktree {
		if err := checkout(s.targetBranch); err != nil {
			return fmt.Errorf("checking out %s: %w", s.targetBranch, err)
		}
	} else {
		if err := os.Chdir(targetWorktree.dir); err != nil {
			return fmt.Errorf("changing to %s: %w", targetWorktree.dir, err)
		}
	}

	if err := pull(); err != nil {
		return fmt.Errorf("pulling from %s: %w", targetWorktree.dir, err)
	}

	if err := s.restore(); err != nil {
		return fmt.Errorf("restoring to the current state: %w", err)
	}

	return nil
}

func (s *state) updateWorktreeBranches() error {
	worktreesToUpdate := make([]worktree, 0, len(s.worktrees))
	for _, w := range s.worktrees {
		if w.branch != s.targetBranch {
			worktreesToUpdate = append(worktreesToUpdate, w)
			continue
		}
	}

	for i, w := range worktreesToUpdate {
		fmt.Printf("  %s [%d/%d]...\n", w.branch, i+1, len(worktreesToUpdate))
		if err := os.Chdir(w.dir); err != nil {
			return err
		}

		if err := rebase(s.targetBranch); err != nil {
			return err
		}
	}

	return nil
}

func (s *state) updateNonworktreeBranches() error {
	branchesToUpdate := make([]string, 0, len(s.branches))
	for _, b := range s.branches {
		if !slices.ContainsFunc(s.worktrees, func(w worktree) bool { return w.branch == b }) {
			branchesToUpdate = append(branchesToUpdate, b)
		}
	}

	for i, b := range branchesToUpdate {
		fmt.Printf("  %s [%d/%d]...\n", b, i+1, len(branchesToUpdate))
		if err := checkout(b); err != nil {
			return err
		}

		if err := rebase(s.targetBranch); err != nil {
			return err
		}
	}

	return nil
}

func (s *state) restore() error {
	if err := os.Chdir(s.currentDir); err != nil {
		return err
	}
	if err := checkout(s.currentBranch); err != nil {
		return err
	}
	return nil
}
