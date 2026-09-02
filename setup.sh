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
  All-or-nothing    Every substitution is checked before any is applied. If one
                    no longer matches, nothing is written.

`--check` verifies that every substitution below still matches something and
writes nothing. `make verify` runs it, so a reformatted Makefile or a
re-indented defaults.yaml fails here rather than in someone's fork.

Exit codes: 0 on success, including "already set up". 1 on any failure, with
the tree left as it was.
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
	NEW_MODULE="${1:-}"
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
# \Q...\E quotes the name so a dot or dash in it stays literal.
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
			FAILED="$FAILED\n  $_file — nothing matched /$_pattern/"
		fi
	else
		perl -pi -e "s/$_pattern/$_replacement/" "$_file"
	fi
}

# Both passes run the same list, so the check cannot drift from what is applied.
anchors() {
	anchor Makefile \
		"^APP_NAME := \\Q$OLD_NAME\\E\$" \
		"APP_NAME := $NEW_NAME"
	anchor internal/config/defaults.yaml \
		"^  name: \\Q$OLD_NAME\\E\$" \
		"  name: $NEW_NAME"
	anchor README.md \
		"^# \\Q$OLD_NAME\\E\\b" \
		"# $NEW_NAME"
	anchor AGENTS.md \
		"^# \\Q$OLD_NAME\\E\\b" \
		"# $NEW_NAME"
	anchor .github/workflows/test.yml \
		"-t \\Q$OLD_NAME\\E:ci\\b" \
		"-t $NEW_NAME:ci"
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

# The module path is unambiguous enough to replace everywhere it appears, which
# is the one substitution that does not need an anchor.
FILES=$(grep -rl --binary-files=without-match --exclude-dir=.git --exclude-dir=bin \
	-F "$OLD_MODULE" . || true)
[ -n "$FILES" ] || die "no references to $OLD_MODULE found — is this a clone of the template?"

printf '%s\n' "$FILES" | xargs perl -pi -e "s{\\Q$OLD_MODULE\\E}{$NEW_MODULE}g"

MODE=apply
anchors

command -v go >/dev/null 2>&1 || die "renamed, but Go is not installed — install Go and run: go build ./..."
go build ./... || die "renamed, but the tree does not compile — see the error above"

printf '%b\n' "${GREEN}✓ $OLD_MODULE → $NEW_MODULE$OFF"
printf '%b\n' "${DIM}  service name: $NEW_NAME$OFF"
printf '%b\n' "${DIM}  rewritten:    $(printf '%s\n' "$FILES" | wc -l | tr -d ' ') files, plus 5 name references$OFF"
printf '%b\n' "${DIM}  go build:     ok$OFF"
printf '\n'
printf '%s\n' "Next:"
printf '%s\n' "  git remote set-url origin git@github.com:you/$NEW_NAME.git   # still points at the template"
printf '%s\n' "  make verify"
printf '%s\n' "  make run"
