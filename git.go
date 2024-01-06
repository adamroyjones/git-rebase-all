package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
)

func branches() ([]string, error) {
	cmd := exec.Command("git", "branch", "--format=%(refname:short)")
	bs, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("running `git branch`: %w", err)
	}

	branches := []string{}
	scanner := bufio.NewScanner(bytes.NewReader(bs))
	for scanner.Scan() {
		branches = append(branches, strings.TrimSpace(scanner.Text()))
	}
	return branches, nil
}

func checkout(branch string) error {
	if bs, err := exec.Command("git", "checkout", branch).CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(bs))
		pwd, _ := os.Getwd()
		return fmt.Errorf("running `git checkout %s` (message: %s, pwd: %s): %w", branch, msg, pwd, err)
	}
	return nil
}

func currentBranch() (string, error) {
	bs, err := exec.Command("git", "branch", "--show-current").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("running `git branch`: %w", err)
	}
	return strings.TrimSpace(string(bs)), nil
}

func fetch() error {
	if err := exec.Command("git", "fetch", "--prune").Run(); err != nil {
		return fmt.Errorf("running `git fetch`: %w", err)
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

func worktrees() ([]worktree, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	bs, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("running `git branch`: %w", err)
	}

	ws := strings.Split(string(bs), "\n\n")
	ws = slices.DeleteFunc(ws, func(s string) bool { return s == "" })

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
