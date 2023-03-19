package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/exp/slices"
)

type worktree struct {
	directory string
	branch    string
}

type currentState struct {
	directory string
	branch    string
}

type state struct {
	worktrees    []worktree
	currentState currentState
	branches     []string
	targetBranch string
}

// TODO: Handle the graph of branches. In particular, if b -> a -> master, make sure that a is rebased onto master and then b is rebased onto a.
// TODO: More gracefully handle errors: abort the rebase and revert the branches.
func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Fatal error: %v.\n", err)
		os.Exit(1)
	}
}

func run() (err error) {
	if !isGitDirectory() {
		return errors.New("command is not being run from a Git directory")
	}

	s, err := newState()
	if err != nil {
		return fmt.Errorf("constructing state struct: %w", err)
	}

	defer func() { err = s.restore(err) }()

	fmt.Printf("Fetching, pruning, and updating '%s'...\n", s.targetBranch)
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

func isGitDirectory() bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	err := cmd.Run()
	return err == nil
}

func newState() (state, error) {
	s := state{}
	var err error
	s.worktrees, err = worktrees()
	if err != nil {
		return s, fmt.Errorf("fetching and parsing worktrees: %w", err)
	}

	s.branches, err = branches()
	if err != nil {
		return s, fmt.Errorf("finding current branches: %w", err)
	}

	currentBranch, err := currentBranch()
	if err != nil {
		return s, fmt.Errorf("finding current branch: %w", err)
	}

	currentDirectory, err := os.Getwd()
	if err != nil {
		return s, fmt.Errorf("fetching current directory: %w", err)
	}

	s.currentState = currentState{directory: currentDirectory, branch: currentBranch}

	// This should always be true, but it's a sanity check.
	if !s.currentStateIsWorktree() {
		return s, fmt.Errorf("current state (%+v) not in the worktrees (%+v)", s.currentState, s.worktrees)
	}

	hasMaster := slices.Contains(s.branches, "master")
	hasMain := slices.Contains(s.branches, "main")
	if hasMaster && hasMain {
		return s, errors.New("unexpected situation: the branch has both `master` and `main` branches")
	}

	s.targetBranch = "master"
	if hasMain {
		s.targetBranch = "main"
	}

	return s, nil
}

func worktrees() ([]worktree, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	bs, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("running `git branch`: %w", err)
	}

	s := string(bs)
	ws := strings.Split(s, "\n\n")
	// Remove any empty lines.
	{
		i := 0
		for _, w := range ws {
			if strings.TrimSpace(ws[i]) != "" {
				ws[i] = w
				i++
			}
		}
		ws = ws[:i]
	}

	out := make([]worktree, len(ws))
	for i, w := range ws {
		lines := strings.Split(w, "\n")
		if d := len(lines); d != 3 {
			return nil, fmt.Errorf("expected worktree %d to have 3 lines; found %d", i, d)
		}

		before, directory, ok := strings.Cut(lines[0], " ")
		if !ok || before != "worktree" {
			// TODO: Add more error-handling for the case wehere the head is detatched.
			return nil, fmt.Errorf(`expected text in the form "worktree <dir>"; found "%s"`, lines[0])
		}

		before, branchRef, ok := strings.Cut(lines[2], " ")
		if !ok || before != "branch" {
			return nil, fmt.Errorf(`expected text in the form "branch <ref>"; found "%s"`, lines[2])
		}

		branchComponents := strings.Split(branchRef, "/")
		if d := len(branchComponents); d != 3 {
			return nil, fmt.Errorf("expected 3 branch components (e.g. refs/heads/master); found %d", d)
		}
		branch := branchComponents[2]

		out[i] = worktree{directory: directory, branch: branch}
	}

	return out, nil
}

func branches() ([]string, error) {
	cmd := exec.Command("git", "branch", "--format=%(refname:short)")
	bs, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("running `git branch`: %w", err)
	}

	s := string(bs)
	ss := strings.Split(strings.TrimSpace(s), "\n")
	for i := range ss {
		ss[i] = strings.TrimSpace(ss[i])
	}

	return ss, nil
}

func currentBranch() (string, error) {
	cmd := exec.Command("git", "branch", "--show-current")
	bs, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("running `git branch`: %w", err)
	}

	return strings.TrimSpace(string(bs)), nil
}

func (s *state) currentStateIsWorktree() bool {
	for _, w := range s.worktrees {
		// TODO: Make the directory check more robust.
		if strings.HasPrefix(s.currentState.directory, w.directory) && s.currentState.branch == w.branch {
			return true
		}
	}

	return false
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
		if err := os.Chdir(targetWorktree.directory); err != nil {
			return fmt.Errorf("changing to %s: %w", targetWorktree.directory, err)
		}
	}

	if err := pull(); err != nil {
		return fmt.Errorf("pulling from %s: %w", targetWorktree.directory, err)
	}

	if err := s.restore(nil); err != nil {
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

		if err := os.Chdir(w.directory); err != nil {
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

BranchLoop:
	for _, b := range s.branches {
		for _, w := range s.worktrees {
			if w.branch == b {
				continue BranchLoop
			}
		}

		branchesToUpdate = append(branchesToUpdate, b)
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

func fetch(prune bool) error {
	var cmd *exec.Cmd
	if prune {
		cmd = exec.Command("git", "fetch", "--prune")
	} else {
		cmd = exec.Command("git", "fetch")
	}

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("running `git fetch`: %w", err)
	}

	return nil
}

func checkout(branch string) error {
	bs, err := exec.Command("git", "checkout", branch).CombinedOutput()
	msg := strings.TrimSpace(string(bs))
	pwd, _ := os.Getwd()
	if err != nil {
		return fmt.Errorf("running `git checkout %s` (message: %s, pwd: %s): %w", branch, msg, pwd, err)
	}

	return nil
}

func pull() error {
	err := exec.Command("git", "pull").Run()
	if err != nil {
		return fmt.Errorf("running `git pull`: %w", err)
	}

	return nil
}

func rebase(targetBranch string) error {
	err := exec.Command("git", "rebase", targetBranch).Run()
	if err != nil {
		return fmt.Errorf("running `git rebase %s`: %w", targetBranch, err)
	}

	return nil
}

func abortRebase() error {
	err := exec.Command("git", "rebase", "--abort").Run()
	if err != nil {
		return fmt.Errorf("running `git rebase --abort`: %w", err)
	}

	return nil
}

func (s *state) restore(err error) error {
	if chdirErr := os.Chdir(s.currentState.directory); chdirErr != nil {
		return errors.Join(err, chdirErr)
	}
	if checkoutErr := checkout(s.currentState.branch); checkoutErr != nil {
		return errors.Join(err, checkoutErr)
	}
	return err
}
