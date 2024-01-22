# git-rebase-all

This program rebases all branches across all worktreees for the current working
directory's git repository.

- [Current status](#current-status)
- [Prerequisites](#prerequisites)
- [Why does this program exist?](#why-does-this-program-exist?)
- [Installing](#installing)
- [Usage](#usage)

## Current status

This currently works for my purposes, but there are a few more steps to take.

Firstly, my usual branching structure is such that every branch that isn't
master has at most one "direct" child. That is, it assumes a branching structure
like

```
x b
|
|
x a
 \
  \
   x master
```

and not like

```
x b   x c
 \   /
  \ /
   x a
    \
     \
      x master
```

When I can find the time, I'll generalise this program to handle to arbitrary
branching trees.

Secondly, the program should suspend operations, permit manual fixups, and
support `--continue` and `--abort` flags.

Thirdly, the error messages should be more helpful. Dumping the output from git
isn't all that friendly.

It could also do with an integration test suite.

## Prerequisites

As it relies on `git rebase --update-refs`, this program requires git 2.38+.

## Why does this program exist?

If you've a number of worktrees and branches in-flight, and if there's a target
branch (e.g., master) that moves frequently, and if you prefer to keep your
branches rebased onto that target branch, then you've probably found that it's
tedious to repeatedly rebase your branches.

It's still more tedious if you want to ensure that the graphical structure is
preserved after rebasing; that is, to preserve the fact that if `a -> b` before
rebasing `a` and `b` onto the target branch, then `a -> b` after so rebasing.

The (trivial) program in this repository attempts to make this process simpler.

## Installing

This requires a Go toolchain.

```sh
go install github.com/adamroyjones/git-rebase-all@latest
```

## Usage

```sh
git-rebase-all -h
```
