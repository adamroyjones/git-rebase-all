package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/exp/slices"
)

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

	branches, err := branches()
	if err != nil {
		return fmt.Errorf("finding current branches: %w", err)
	}

	currentBranch, err := currentBranch()
	if err != nil {
		return fmt.Errorf("finding current branch: %w", err)
	}

	fmt.Println("Fetching...")
	if err := fetch(); err != nil {
		return fmt.Errorf("fetching from the remote: %w", err)
	}

	hasMaster := slices.Contains(branches, "master")
	hasMain := slices.Contains(branches, "main")
	if hasMaster && hasMain {
		return errors.New("unexpected situation: the branch has both `master` and `main` branches")
	}
	targetBranch := "master"
	if hasMain {
		targetBranch = "main"
	}

	for i, b := range branches {
		if b == targetBranch {
			branches = append(branches[:i], branches[i+1:]...)
			break
		}
	}

	fmt.Printf("Checking out and pulling %s...\n", targetBranch)
	if err := checkout(targetBranch); err != nil {
		return fmt.Errorf("checking out %s: %w", targetBranch, err)
	}
	if err := pull(); err != nil {
		return fmt.Errorf("pulling: %w", err)
	}

	defer func() {
		fmt.Printf("Finished: checking out %s...\n", currentBranch)
		checkoutErr := checkout(currentBranch)
		if checkoutErr == nil {
			return
		}

		if err == nil {
			err = fmt.Errorf("checking out %s: %w", currentBranch, checkoutErr)
			return
		}

		err = fmt.Errorf("%w; also, error checking out %s: %v", err, currentBranch, checkoutErr)
		return
	}()

	fmt.Println("Rebasing...")
	for i, branch := range branches {
		fmt.Printf("  Rebasing %s onto %s... (%d/%d)\n", branch, targetBranch, i+1, len(branches))
		err = rebase(branch, targetBranch)
		if err != nil {
			abortErr := abortRebase()
			if abortErr == nil {
				return fmt.Errorf("rebasing %s: %w; successfully aborted", branch, err)
			}

			return fmt.Errorf("rebasing %s: %w; also, error aborting: %v", branch, err, abortErr)
		}
	}

	// TODO: Handle worktrees.
	return nil
}

func isGitDirectory() bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	err := cmd.Run()
	return err == nil
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

func fetch() error {
	err := exec.Command("git", "fetch").Run()
	if err != nil {
		return fmt.Errorf("running `git fetch`: %w", err)
	}

	return nil
}

func checkout(branch string) error {
	err := exec.Command("git", "checkout", branch).Run()
	if err != nil {
		return fmt.Errorf("running `git checkout %s`: %w", branch, err)
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

func rebase(branch, targetBranch string) error {
	if err := checkout(branch); err != nil {
		return err
	}

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
