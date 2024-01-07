package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"slices"
	"strings"
)

func branches(dir string) ([]string, error) {
	cmd := exec.Command("git", "branch", "--format=%(refname:short)")
	cmd.Dir = dir
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

func branchChildren(dir, branch string) ([]string, error) {
	cmd := exec.Command("git", "branch", "--contains", branch, "--format=%(refname:short)")
	cmd.Dir = dir
	bs, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("unable to list the branches that contain %s: %w", branch, err)
	}

	lines := strings.Split(string(bs), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// The code detaches all of the HEADs and performs the Git operations in
		// that state. The command `git branch --contains master` (say) will
		// include the detached HEAD in the list; this should be excluded.
		// TODO: Is there a better way of avoiding this?
		if line == "" || line == branch || strings.Contains(line, "HEAD detached") {
			continue
		}
		out = append(out, line)
	}
	return out, nil
}

func checkout(dir, branch string) error {
	cmd := exec.Command("git", "checkout", branch)
	cmd.Dir = dir
	if bs, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(bs))
		return fmt.Errorf("running `git checkout %s` (message: %s, dir: %s): %w", branch, msg, dir, err)
	}
	return nil
}

func currentBranch(dir string) (string, error) {
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = dir
	bs, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("running `git branch`: %w", err)
	}
	return strings.TrimSpace(string(bs)), nil
}

func decapitate(dir string) error {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	bs, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("determining the commit SHA (dir: %s): %w", dir, err)
	}

	sha := strings.TrimSpace(string(bs))
	cmd = exec.Command("git", "checkout", sha)
	cmd.Dir = dir
	if bs, err = cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("detaching the HEAD (dir: %s): %w (output: %s)", dir, err, strings.TrimSpace(string(bs)))
	}
	return nil
}

func fetch(dir string) error {
	cmd := exec.Command("git", "fetch", "--prune")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("running `git fetch`: %w", err)
	}
	return nil
}

func pull(dir string) error {
	cmd := exec.Command("git", "pull")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("running `git pull`: %w", err)
	}
	return nil
}

func rebase(dir, targetBranch string) error {
	// --update-refs is what permits us to restrict ourselves to the leaves.
	cmd := exec.Command("git", "rebase", targetBranch, "--update-refs")
	cmd.Dir = dir
	bs, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}

	output := strings.TrimSpace(string(bs))
	err = fmt.Errorf("failed to rebase %q (output: %s): %w", targetBranch, output, err)

	cmd = exec.Command("git", "rebase", "--abort")
	cmd.Dir = dir
	abortBs, abortErr := cmd.CombinedOutput()
	if abortErr == nil {
		return fmt.Errorf("%w; successfully aborted", err)
	}

	abortOutput := strings.TrimSpace(string(abortBs))
	abortErr = fmt.Errorf("failed to abort the rebase: %w (output: %s)", abortErr, abortOutput)
	return fmt.Errorf("%w; %w", err, abortErr)
}

func status(dir string) ([]string, error) {
	cmd := exec.Command("git", "status", "--porcelain=v1")
	cmd.Dir = dir
	bs, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("running `git status`: %w", err)
	}

	ss := strings.Split(strings.TrimSpace(string(bs)), "\n")
	return slices.DeleteFunc(ss, func(s string) bool { return s == "" }), nil
}

// worktrees returns the set of worktrees. It will return an error if there
// exists a worktree that isn't a checked-out branch.
func worktrees() ([]worktree, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain", "-z")
	bs, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("running `git branch`: %w (output: %s)", err, strings.TrimSpace(string(bs)))
	}

	ws := strings.Split(string(bs), "\x00\x00")
	ws = slices.DeleteFunc(ws, func(s string) bool { return s == "" })

	out := make([]worktree, len(ws))
	for i, w := range ws {
		lines := strings.Split(w, "\x00")
		if d := len(lines); d != 3 {
			return nil, fmt.Errorf("expected worktree %d to have 3 lines; found %d", i, d)
		}

		before, dir, ok := strings.Cut(lines[0], " ")
		if !ok || before != "worktree" {
			return nil, fmt.Errorf(`expected text in the form "worktree <dir>"; found %q`, lines[0])
		}

		before, branchRef, ok := strings.Cut(lines[2], " ")
		if !ok || before != "branch" {
			return nil, fmt.Errorf(`expected text in the form "branch <ref>"; found %q (dir: %s)`, lines[2], dir)
		}

		branchComponents := strings.Split(branchRef, "/")
		if d := len(branchComponents); d < 3 {
			return nil, fmt.Errorf("expected at least 3 branch components (e.g. refs/heads/master); found %d", d)
		}
		branch := strings.Join(branchComponents[2:], "/")

		out[i] = worktree{dir: dir, branch: branch}
	}

	return out, nil
}
