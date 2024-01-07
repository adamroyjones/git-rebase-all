# git-rebase-all

This program rebases all branches across all worktreees for the current working
directory.

- [Prerequisites](#prerequisites)
- [Why does this program exist?](#why-does-this-program-exist?)
- [Installing](#installing)
- [Usage](#usage)

## Prerequisites

This has been used with git version 2.39.2. This is the version of git in the
package repository for Debian Bookworm as of 2024-01-07.

I believe it'll work with git version 2.38+. As it relies on `git rebase --update-refs`,
it won't work with earlier versions.

## Why does this program exist?

If you've a number of worktrees and branches in-flight, and if there's a target
branch (e.g., master) that moves frequently, and if you prefer to keep your
branches rebased onto that target branch, then you've probably found that it's
tedious to repeatedly rebase the local branches. It's especially awkward if you
want to ensure that the graphical structure is preserved after rebasing, that
is, to preserve the fact that if `a -> b` before rebasing `a` and `b` onto the
target branch, then `a -> b` after so rebasing.

This program attempts to make this process a little less tedious.

## Installing

This requires a Go toolchain.

```sh
go install github.com/adamroyjones/git-rebase-all@latest
```

## Usage

```sh
git-rebase-all -h
```
