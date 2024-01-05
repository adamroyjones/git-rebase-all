package main

import (
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
		return errors.New("command is not being run from a Git directory")
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

func worktrees() ([]worktree, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	bs, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("running `git branch`: %w", err)
	}

	ws := strings.Split(string(bs), "\n\n")
	slices.DeleteFunc(ws, func(s string) bool { return s == "" })

	out := make([]worktree, len(ws))
	for i, w := range ws {
		lines := strings.Split(w, "\n")
		if d := len(lines); d != 3 {
			return nil, fmt.Errorf("expected worktree %d to have 3 lines; found %d", i, d)
		}

		before, directory, ok := strings.Cut(lines[0], " ")
		if !ok || before != "worktree" {
			// TODO: Add more error-handling for the case where HEAD is detatched.
			return nil, fmt.Errorf(`expected text in the form "worktree <dir>"; found "%s"`, lines[0])
		}

		before, branchRef, ok := strings.Cut(lines[2], " ")
		if !ok || before != "branch" {
			return nil, fmt.Errorf(`expected text in the form "branch <ref>"; found "%s"`, lines[2])
		}

		branchComponents := strings.Split(branchRef, "/")
		if d := len(branchComponents); d < 3 {
			return nil, fmt.Errorf("expected at least 3 branch components (e.g. refs/heads/master); found %d", d)
		}
		branch := strings.Join(branchComponents[2:], "/")

		out[i] = worktree{dir: directory, branch: branch}
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
	bs, err := exec.Command("git", "branch", "--show-current").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("running `git branch`: %w", err)
	}
	return strings.TrimSpace(string(bs)), nil
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

func fetch() error {
	if err := exec.Command("git", "fetch", "--prune").Run(); err != nil {
		return fmt.Errorf("running `git fetch`: %w", err)
	}

	return nil
}

func checkout(branch string) error {
	if bs, err := exec.Command("git", "checkout", branch).CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(bs))
		pwd, _ := os.Getwd()
		return fmt.Errorf("running `git checkout %s` (message: %s, pwd: %s): %w", branch, msg, pwd, err)
	}
	return nil
}

func pull() error {
	if err := exec.Command("git", "pull").Run(); err != nil {
		return fmt.Errorf("running `git pull`: %w", err)
	}
	return nil
}

func rebase(targetBranch string) error {
	if err := exec.Command("git", "rebase", targetBranch).Run(); err != nil {
		return fmt.Errorf("running `git rebase %s`: %w", targetBranch, err)
	}
	return nil
}

func abortRebase() error {
	if err := exec.Command("git", "rebase", "--abort").Run(); err != nil {
		return fmt.Errorf("running `git rebase --abort`: %w", err)
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
