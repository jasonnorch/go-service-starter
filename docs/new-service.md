# Starting a new service from this template

For whoever has been told to build a service on go-service-starter, person or
agent. Follow it in order. The end state is a repository that is the new
service's and nothing else's: history that begins at one import commit, its own
module path, its own README, and none of the machinery that exists only to turn
the template into something else.

**Do not clone this repo and start working in the clone.** That is the mistake
this file exists to prevent. The result builds, passes `make verify`, and is
still this repo: its history, its `origin`, its README, its instructions for
forking itself.

Two things have to be known before starting, and neither can be guessed:

- **The module path, in full**: `github.com/you/yourthing`. The service name is
  its last segment.
- **Which modules to keep.** `postgres`, `cache` and `auth` are all included
  until removed; "Modules" in the README says what each one is. Decide now.
  Step 3 removes a module with one flag; after step 4 it is a job by hand.

If you were not told either one, ask before you start.

## 1. Export the tree, without its history

```sh
git clone --depth 1 https://github.com/jasonnorch/go-service-starter /tmp/starter-src
git -C /tmp/starter-src rev-parse --short HEAD     # keep it for the commit message
mkdir -p path/to/yourthing
git -C /tmp/starter-src archive HEAD | tar -x -C path/to/yourthing
```

The clone is something to copy from, never the project. Any empty scratch path
will do for it, and so will a clone you already made in order to read this, as
long as it is at the tip of `main`. `git archive` writes the tracked files and
no `.git`, so there is no history to reset afterwards and no `origin` to
repoint, and nothing destructive ever has to be run inside the new repo.

**Keep that clone until you finish, and read this file from it.** The copy that
lands in the new repo does not last: `setup.sh` rewrites the URL above along
with every other mention of the module path, and step 6 deletes the file.

If the destination already holds files, because someone created the repo ahead
of you, look for collisions first. `tar` overwrites without asking:

```sh
git -C /tmp/starter-src ls-files -z | (cd path/to/yourthing && xargs -0 ls -d 2>/dev/null)
```

Anything that prints is a file of theirs the export would replace. An empty
placeholder is yours to overwrite. Anything with content is not: stop and say
what collides.

## 2. Commit the import

```sh
cd path/to/yourthing
git init -b main          # skip this in a directory that is already a repository
git add -A
git commit -m "Import go-service-starter@<sha>"
```

This is not optional, and it is not folded into a later commit. `setup.sh`
works on tracked files and uses git as its undo, so it refuses a tree whose
import is not committed. A commit that is the template verbatim is also worth
having for its own sake: `git diff <that commit>` is everything this service
changed, which is what you want in hand when a later fix to the template needs
carrying over.

The messages here and in step 6 are subject lines. Add whatever trailers or
sign-offs the rules you work under require. In a new repository both commits go
straight to `main`, since there is nothing yet to open a pull request against.
In an existing repo that takes changes through branches, do this and everything
after it on a branch.

## 3. Rename, and drop the modules you do not want

```sh
./setup.sh github.com/you/yourthing --without cache,auth
make verify
```

Leave off `--without` to keep all three. Never rename by hand, and never `sed`
the name across the tree; `AGENTS.md` says what that breaks. Success ends with
`go build, go vet: ok`. On a failure, read the error and stop rather than
working around it. `git restore .` undoes whatever was written.

`make verify` here is the last point at which a failure can only be the
script's. After step 4 a failure may be your own edit.

## 4. Detach from the template

`setup.sh` has now done the one job it will ever do here. It, the markers it
reads, and the target that tests it are all machinery for producing a service
from the template, and this is the service. Remove them:

- **`setup.sh`**: delete it.
- **`Makefile`**:
  - the comment above `APP_NAME`;
  - in `verify`, the two lines for `./setup.sh --check` (the `printf` that
    labels it and the call), and the paragraph above the target that describes
    them. Miss these and `make verify` fails on a script that is not there;
  - the whole `verify-without` target with its comment, and its name in
    `.PHONY`;
  - the comment above `test-integration` explaining where the markers sit.
- **`.github/workflows/test.yml`**: the `without` job.
- **`.dockerignore`**: the `setup.sh` line.
- **Every module marker**, except in `README.md`, which step 5 replaces. A
  marker is a comment reading `module:<names>`, sometimes followed by `begin`
  or `end`. They told `setup.sh` what to delete, and nothing reads them now.
  `git grep -nE 'module:[a-z]'` lists them.
  - A marker alone on its line: delete the line.
  - A marker after code or text: delete the comment and the space before it.
    **The line stays.** The marker goes; what it marked is the service.

  Then run `gofmt -l .` and `gofmt -w` whatever it lists. A trailing comment is
  part of how gofmt aligns a block, so removing one can shift its neighbours.

## 5. Rewrite the docs that still describe the template

These are prose, so they take judgment rather than a list of lines.

- **`README.md`**: replace it. What is there is a pitch for the template. The
  service's README says what the service is for and how to run it. Where the
  service does not do its job yet, say so; do not describe endpoints that are
  not written. The Configuration, Endpoints, Make targets and Layout sections
  are still true and worth keeping, trimmed of `setup.sh` and
  `verify-without`. The rest goes: the pitch, "What you get", "Why stdlib and
  not a framework", "Swapping the opinionated parts", "Modules", "Not
  included" and "License".
- **`AGENTS.md`**:
  - Rewrite the opening to say what the service is, and delete the "This repo
    is a template" paragraph.
  - Delete the `setup.sh` and `docs/new-service.md` rows in "Where things
    are", the `./setup.sh` and `make verify-without` bullets under "Commands",
    and all of "Forking this template".
  - Retitle "Modules" to "Adding a dependency". Keep its paragraph that begins
    "A module lands as its own package", and its last three bullets: close
    with a defer in `app.Run`, fail at startup when the dependency is
    unreachable, and keep tests that need the real thing behind the
    `integration` tag. Delete the rest of the section, which is about markers.
  - "Rules that are load-bearing" and "Safety boundaries" stay as they are.
    They are about the code, and the code came with you.
- **`CLAUDE.md`**: delete the bullet that begins "This is a template repo".
- **`docs/decisions.md`**: delete "setup.sh instead of make rename" and
  "Modules, and removing them with --without". Reword the introduction, and
  add one line saying the decisions below were inherited from
  go-service-starter at the commit you imported. That line is the service's
  only record of where it came from. The remaining sections stay: they are why
  the code is the way it is.
- **Comments that mention `setup.sh`**: a handful, in Go files, `compose.yaml`,
  `internal/config/defaults.yaml` and `docs/decisions.md`. The first grep in
  step 6 finds them. Reword each to say what is true now.
- **Prose that names a module you removed.** `--without` removes marked lines.
  It does not rewrite a sentence elsewhere that mentions the module in passing:
  a comment naming a package that is gone, "Postgres and Redis" where only
  Postgres is left. Grep for each removed module's name, and for what it stood
  for (`redis` as well as `cache`), and fix what now reads wrong.

Some of what is left belongs to the template's owner rather than to the
template: `LICENSE` (MIT, in the author's name), `.coderabbit.yaml`, the
"Conventions" section of `AGENTS.md` (pull requests, the review bot, commit
attribution), and comments in `Makefile` and `.golangci.yml` about a fleet of
repos. A service with the same owner keeps all of it. If the owner is someone
else, or you were not told, leave these alone and list them in your report as
decisions waiting for the owner. Do not guess a license.

## 6. Check, then commit

Delete this file, `docs/new-service.md`, from the new repo. Then:

```sh
git grep -nE 'setup\.sh|verify-without|new-service\.md|module:[a-z]|[Mm]arker'
git grep -n 'go-service-starter'
```

The lists in steps 4 and 5 will drift from the tree; these two commands are the
authority. The first prints nothing. The second prints one line, the provenance
line in `docs/decisions.md`. The words "template", "starter" and "fork" will
still turn up in comments and in the reasoning in `docs/decisions.md`. They
read fine as history. Reword one only where it gives an instruction that no
longer applies.

Then run the gate and commit:

```sh
make verify
git add -A
git commit -m "Set up yourthing from go-service-starter@<sha>"
```

`make verify` has to pass here, on the detached tree. Report its real output.

## 7. The remote

`git remote -v` prints nothing at this point, which is correct. Creating the
remote repository and pushing to it are the owner's steps unless you were told
to do them. When there is one:

```sh
git remote add origin git@github.com:you/yourthing.git
```

## If you already cloned

The signs: `git remote -v` names the template, or `git log` shows the
template's merge commits under your own. Nothing needs deleting to fix it.
Commit what you have, then export your own tree the same way step 1 exports
the template's:

```sh
mkdir -p path/to/fresh
git archive HEAD | tar -x -C path/to/fresh
```

Carry on from step 2 in the new directory, skipping whatever is already done:
`setup.sh` prints `already <module>` when the rename has happened, and step 4
onward applies as written. Only committed, tracked files travel, so check `git
status` first for anything untracked that the service needs.

Leave the old clone where it is and say that it is there. If it was already
pushed somewhere, that remote holds the template's history too, and replacing
it is a force-push: the owner's call, not a step to take on the way past.
