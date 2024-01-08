package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"slices"
	"strings"
)

func branchToSHA(dir, branch string) (string, error) {
	cmd := exec.Command("git", "rev-parse", branch)
	cmd.Dir = dir
	bs, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("running `git rev-parse`: %w", err)
	}
	return strings.TrimSpace(string(bs)), nil
}

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
		// The code detaches all of the HEADs and performs the git operations in
		// that state. The command `git branch --contains master` (say) will include
		// both master and the detached HEAD in the list. We should exclude them.
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
		output := strings.TrimSpace(string(bs))
		return fmt.Errorf("running `git checkout %s` (dir: %s): %w (output: %s)", branch, dir, err, output)
	}
	return nil
}

func decapitate(dir string) error {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	bs, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("determining the commit SHA (dir: %s): %w (output: %s)", dir, err, strings.TrimSpace(string(bs)))
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
	// The --update-refs flag permits us to restrict our interest to the leaves.
	cmd := exec.Command("git", "rebase", targetBranch, "--update-refs")
	cmd.Dir = dir
	bs, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}

	// If the above fails, we should abort the rebase.
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
	return slices.DeleteFunc(ss, func(s string) bool { return s == "" || strings.HasPrefix(s, "??") }), nil
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
	out := make([]worktree, 0, len(ws))
	for _, w := range ws {
		lines := strings.Split(w, "\x00")
		if d := len(lines); d != 3 {
			return nil, fmt.Errorf("expected the worktree to have 3 lines; found %d (output: %s)", d, strings.Join(lines, "\n"))
		}

		var dir, branch string
		if _, err := fmt.Sscanf(lines[0], "worktree %s", &dir); err != nil {
			return nil, fmt.Errorf(`expected text in the form "worktree <dir>"; found %q (dir: %s)`, lines[0], dir)
		}
		if _, err := fmt.Sscanf(lines[2], "branch refs/heads/%s", &branch); err != nil {
			return nil, fmt.Errorf(`expected text in the form "branch refs/heads/<branch>"; found %q (dir: %s)`, lines[2], dir)
		}
		out = append(out, worktree{dir: dir, branch: branch})
	}
	return out, nil
}
