#!/bin/sh
# setup.sh — the first thing you run in a fresh clone of this template.
#
# Rewrites the module path and every place the starter's own name is baked in,
# then compiles the result to prove it. It lives at the repo root, with the
# extension, because the reader is a stranger and a stranger's first move is
# `ls`.
#
# Written so an agent can run it as safely as a person can: it never prompts,
# never deletes anything, and is a no-op once the module already matches. See
# docs/decisions.md, "setup.sh instead of make rename".

set -eu

usage() {
	cat <<'USAGE'
usage: ./setup.sh <module-path>
       ./setup.sh --check

Renames this template to your own module, then runs `go build ./...` to prove
the result still compiles.

  ./setup.sh github.com/you/yourthing

The argument is a full module path, not a bare name: it is what goes in go.mod
and what every import in the tree becomes. The service name is its last segment
("yourthing"), which is what the service logs on every line and reports from
/info.

Behavior worth relying on:

  Non-interactive   It never prompts, so it cannot hang an unattended run.
  Non-destructive   It rewrites tracked files in place and touches nothing
                    else. Your git history and remotes are left alone.
  Idempotent        Running it when the module already matches is a no-op that
                    exits 0, so a re-run after a failure is always safe.
  Checked first     Every substitution is checked, and Go is confirmed
                    installed, before any file is written. If either fails,
                    nothing is written.

`--check` verifies that every substitution below still matches something and
writes nothing. `make verify` runs it, so a reformatted Makefile or a
re-indented defaults.yaml fails here rather than in someone's fork.

Exit codes: 0 on success, including "already set up". 1 on any failure. A
failure before the first write leaves the tree as it was. A failure after it —
a write that did not land, or `go build` rejecting the result — leaves the
rename in place so the error can be read against it, and says so; `git restore
.` undoes it, since only tracked files are written.
USAGE
}

case "${1:-}" in
-h | --help | help)
	usage
	exit 0
	;;
esac

if [ -t 1 ]; then
	GREEN='\033[32m'
	RED='\033[31m'
	DIM='\033[2m'
	OFF='\033[0m'
else
	GREEN='' RED='' DIM='' OFF=''
fi

die() {
	printf '%b%s%b\n' "$RED" "setup.sh: $1" "$OFF" >&2
	exit 1
}

# The repo root is wherever this script lives, so ./setup.sh works from a
# subdirectory and the file paths below stay relative to the right place.
cd "$(dirname "$0")"

CHECK_ONLY=0
if [ "${1:-}" = "--check" ]; then
	CHECK_ONLY=1
	shift
fi

# Exactly one module path, or nothing after --check. A stray argument is refused
# rather than ignored: `./setup.sh github.com/you/x --check` reads like a check,
# and silently running the rename instead is the worst way to resolve it.
if [ $# -ne $((1 - CHECK_ONLY)) ]; then
	usage >&2
	exit 1
fi

# Only tracked files are rewritten, so git is both the file list and the undo.
git rev-parse --is-inside-work-tree >/dev/null 2>&1 ||
	die "not a git checkout — setup.sh rewrites tracked files, so it needs one"

[ -f go.mod ] || die "no go.mod here — run this from a clone of the template"

# Read the current path from go.mod rather than hardcoding it. That is what
# makes the script idempotent, and reading the file directly rather than asking
# `go list -m` means the check works before any Go toolchain is resolved.
OLD_MODULE=$(awk '$1 == "module" { print $2; exit }' go.mod)
[ -n "$OLD_MODULE" ] || die "could not read the module path from go.mod"

if [ "$CHECK_ONLY" = 1 ]; then
	# Substituting the current name for itself, so the check exercises the real
	# patterns without depending on a target module being supplied.
	NEW_MODULE=$OLD_MODULE
else
	NEW_MODULE=$1
	[ -n "$NEW_MODULE" ] || {
		usage >&2
		exit 1
	}

	# A bare name is a legal Go module path but almost never what was meant, and
	# it is the mistake to expect from someone told only "call it billing-api".
	# Catch it here rather than committing a go.mod nobody can `go get`.
	case "$NEW_MODULE" in
	*[[:space:]]*) die "module path must not contain whitespace: $NEW_MODULE" ;;
	*/*) ;;
	*) die "expected a full module path like github.com/you/$NEW_MODULE, not a bare name" ;;
	esac

	if [ "$OLD_MODULE" = "$NEW_MODULE" ]; then
		printf '%b\n' "${GREEN}✓ already $NEW_MODULE$OFF"
		exit 0
	fi
fi

# The service name is the last path segment, minus a major-version suffix:
# github.com/you/thing/v2 is still "thing", not "v2".
module_name() {
	printf '%s\n' "$1" | sed 's:/v[0-9][0-9]*$::' | sed 's:.*/::'
}
OLD_NAME=$(module_name "$OLD_MODULE")
NEW_NAME=$(module_name "$NEW_MODULE")

# Every value reaches perl through the environment, never spliced into the
# program text, so the program is fixed and the names are data. Spliced in, a
# `$&` or `@x` in a module path would be expanded by perl rather than written.
export OLD_MODULE NEW_MODULE OLD_NAME NEW_NAME

# Anchored, position-specific substitutions for the places that carry the bare
# name rather than the module path.
#
# They have to be anchored. A tree-wide swap of the bare name is a silent
# disaster: fork this to github.com/you/api and the old name is "api", which
# appears inside "httpapi" in half the files here. Each pattern below names one
# line in one file, and a pattern that stops matching is an error rather than a
# quiet no-op — a substitution that silently does nothing is the exact bug this
# script exists to fix.
#
# perl rather than `sed -i`: the backup-suffix argument must be empty on
# BSD/macOS and absent on GNU, and there is no spelling that works on both.
# The patterns are single-quoted perl that reads the names from $ENV;
# \Q...\E quotes the old name so a dot or dash in it stays literal.
FAILED=''
MODE=check

anchor() {
	_file=$1
	_pattern=$2
	_replacement=$3

	if [ "$MODE" = check ]; then
		if [ ! -f "$_file" ]; then
			FAILED="$FAILED\n  $_file — file not found"
		elif ! perl -0777 -ne "exit(/$_pattern/m ? 0 : 1)" "$_file"; then
			# The name, not the pattern: FAILED is printed with %b, which would
			# eat the pattern's backslashes, and $ENV{...} tells a reader nothing.
			FAILED="$FAILED\n  $_file — its anchored line does not carry \"$OLD_NAME\""
		fi
	else
		# Collected rather than fatal, so a failed write reaches the recheck
		# below and its undo hint instead of exiting with the tree half-renamed.
		perl -pi -e "s/$_pattern/$_replacement/" "$_file" ||
			FAILED="$FAILED\n  $_file — write failed"
	fi
}

# Both passes run the same list, so the check cannot drift from what is applied.
# SC2016: the single quotes are the point — perl expands $ENV, the shell must not.
# shellcheck disable=SC2016
anchors() {
	anchor Makefile \
		'^APP_NAME := \Q$ENV{OLD_NAME}\E$' \
		'APP_NAME := $ENV{NEW_NAME}'
	anchor internal/config/defaults.yaml \
		'^  name: \Q$ENV{OLD_NAME}\E$' \
		'  name: $ENV{NEW_NAME}'
	anchor README.md \
		'^# \Q$ENV{OLD_NAME}\E\b' \
		'# $ENV{NEW_NAME}'
	anchor AGENTS.md \
		'^# \Q$ENV{OLD_NAME}\E\b' \
		'# $ENV{NEW_NAME}'
	anchor .github/workflows/test.yml \
		'-t \Q$ENV{OLD_NAME}\E:ci\b' \
		'-t $ENV{NEW_NAME}:ci'
}

anchors
[ -z "$FAILED" ] || {
	printf '%b%s%b' "$RED" "setup.sh: these substitutions no longer match, so nothing was written:" "$OFF" >&2
	printf '%b\n' "$FAILED" >&2
	die "fix the patterns in setup.sh, or rename by hand"
}

if [ "$CHECK_ONLY" = 1 ]; then
	printf '%b\n' "${GREEN}✓ setup.sh anchors match$OFF"
	exit 0
fi

# Checked before the first write, so a missing toolchain leaves the tree
# untouched rather than renamed and unverified.
command -v go >/dev/null 2>&1 || die "Go is not installed — install it and run setup.sh again; nothing was written"

# The module path is unambiguous enough to replace everywhere it appears, which
# is the one substitution that does not need an anchor. git grep rather than
# grep -r: an untracked note or local config that mentions the path is not the
# template's to rewrite, and it skips binaries, .git and ignored build output.
git grep -qF "$OLD_MODULE" || die "no references to $OLD_MODULE found — is this a clone of the template?"
REWRITTEN=$(git grep -lF "$OLD_MODULE" | wc -l | tr -d ' ')

UNDO="the rename is left in place; \`git restore .\` undoes it"

# shellcheck disable=SC2016
git grep -lzF "$OLD_MODULE" | xargs -0 perl -pi -e 's{\Q$ENV{OLD_MODULE}\E}{$ENV{NEW_MODULE}}g' ||
	die "rewriting the module path failed — $UNDO"

MODE=apply
anchors

# Recheck that every write landed. perl -pi only warns when it cannot write a
# file and still exits 0, so a read-only README would otherwise be skipped, the
# build would pass, and the script would report a rename that is not complete.
# Running the check pass with the new name as the old one asserts each anchor
# now carries it; go.mod stands in for the module rewrite, and go build checks
# the imports.
OLD_NAME=$NEW_NAME
MODE=check
anchors
[ "$(awk '$1 == "module" { print $2; exit }' go.mod)" = "$NEW_MODULE" ] ||
	FAILED="$FAILED\n  go.mod — module line was not rewritten"
[ -z "$FAILED" ] || {
	printf '%b%s%b' "$RED" "setup.sh: these writes did not land:" "$OFF" >&2
	printf '%b\n' "$FAILED" >&2
	die "$UNDO"
}

# A failed build is deliberately not rolled back. The compile error points at
# files in the renamed tree, which is where it has to be read, and every write
# above went to a tracked file, so git already is the undo.
go build ./... || die "renamed, but the tree does not compile — see the error above; $UNDO"

printf '%b\n' "${GREEN}✓ $OLD_MODULE → $NEW_MODULE$OFF"
printf '%b\n' "${DIM}  service name: $NEW_NAME$OFF"
printf '%b\n' "${DIM}  rewritten:    $REWRITTEN files, plus 5 name references$OFF"
printf '%b\n' "${DIM}  go build:     ok$OFF"
printf '\n'
printf '%s\n' "Next:"
printf '%s\n' "  git remote set-url origin git@github.com:you/$NEW_NAME.git   # still points at the template"
printf '%s\n' "  make verify"
printf '%s\n' "  make run"
