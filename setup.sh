#!/bin/sh
# setup.sh — the first thing you run in a fresh clone of this template.
#
# Rewrites the module path and every place the starter's own name is baked in,
# removes any modules named with --without, then compiles the result to prove
# it. It lives at the repo root, with the extension, because the reader is a
# stranger and a stranger's first move is `ls`.
#
# Written so an agent can run it as safely as a person can: it never prompts,
# deletes only the modules it is told to, and is a no-op once the tree already
# matches. See docs/decisions.md, "setup.sh instead of make rename" and
# "Modules, and removing them with --without".

set -eu

usage() {
	cat <<'USAGE'
usage: ./setup.sh <module-path> [--without <module>[,<module>...]]
       ./setup.sh --without <module>[,<module>...]
       ./setup.sh --check
       ./setup.sh --modules

Renames this template to your own module, removes the modules you do not
want, then runs `go build ./...` and `go vet ./...` to prove the result still
compiles.

  ./setup.sh github.com/you/yourthing
  ./setup.sh github.com/you/yourthing --without postgres

The argument is a full module path, not a bare name: it is what goes in go.mod
and what every import in the tree becomes. The service name is its last segment
("yourthing"), which is what the service logs on every line and reports from
/info.

--without removes modules: their packages, and every line of wiring, config,
CI and docs marked as theirs. It works on a tree that is already renamed, so a
module can be dropped long after setup. `--modules` lists the ones still here.

Behavior worth relying on:

  Non-interactive   It never prompts, so it cannot hang an unattended run.
  Non-destructive   It rewrites tracked files in place and deletes only the
                    tracked files of modules named with --without. Untracked
                    files, git history and remotes are left alone.
  Idempotent        Running it when the module already matches, or naming a
                    module already removed, is a no-op that exits 0, so a
                    re-run after a failure is always safe.
  Checked first     Every substitution and marker is checked, Go is confirmed
                    installed, and tracked files must have no uncommitted
                    changes, before any file is written. If any of these fails,
                    nothing is written.

`--check` verifies that every substitution still matches something and every
module marker is well-formed, and writes nothing. `make verify` runs it, so a
reformatted Makefile or an unclosed marker fails here rather than in someone's
fork.

Exit codes: 0 on success, including "already set up". 1 on any failure. A
failure before the first write leaves the tree as it was. A failure after it —
a write that did not land, or `go build` rejecting the result — leaves the
changes in place so the error can be read against them, and says so;
`git restore .` undoes them, since only tracked files are written.
USAGE
}

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

# --check and --modules stand alone. Anything else takes at most one module
# path and at most one --without list. A stray argument is refused rather than
# ignored: `./setup.sh github.com/you/x --check` reads like a check, and
# silently running the rename instead is the worst way to resolve it.
ACTION=setup
NEW_MODULE=''
WITHOUT=''
while [ $# -gt 0 ]; do
	case $1 in
	-h | --help | help)
		usage
		exit 0
		;;
	--check | --modules)
		if [ "$ACTION" != setup ] || [ -n "$NEW_MODULE$WITHOUT" ] || [ $# -ne 1 ]; then
			usage >&2
			exit 1
		fi
		ACTION=${1#--}
		;;
	--without)
		if [ $# -lt 2 ] || [ -n "$WITHOUT" ] || [ "$ACTION" != setup ]; then
			usage >&2
			exit 1
		fi
		WITHOUT=$2
		shift
		;;
	-*)
		usage >&2
		exit 1
		;;
	*)
		if [ -n "$NEW_MODULE" ] || [ "$ACTION" != setup ]; then
			usage >&2
			exit 1
		fi
		NEW_MODULE=$1
		;;
	esac
	shift
done
if [ "$ACTION" = setup ] && [ -z "$NEW_MODULE$WITHOUT" ]; then
	usage >&2
	exit 1
fi

# Only tracked files are rewritten, so git is both the file list and the undo.
git rev-parse --is-inside-work-tree >/dev/null 2>&1 ||
	die "not a git checkout — setup.sh rewrites tracked files, so it needs one"

[ -f go.mod ] || die "no go.mod here — run this from a clone of the template"

# ---------------------------------------------------------------------------
# Modules
#
# A module is a package plus the lines of wiring, config, CI and docs that
# exist only for it. The lines are marked with a comment naming the module:
#
#   a line containing  module:<names>          is removed with that module
#   everything between module:<names> begin
#                  and module:<names> end      is removed with it, markers too
#
# <names> is one module or a comma-separated list, and a line or block is
# removed when ANY module it names is removed — which is how code that needs
# two modules at once goes away with either. The comment can be //, # or <!--.
#
# module_paths is the other half: the files that are the module outright. Only
# their tracked files are deleted. Adding a module means a case here, a name in
# KNOWN_MODULES, and markers on everything outside these paths that mentions it.
KNOWN_MODULES=postgres

module_paths() {
	case $1 in
	postgres) echo internal/postgres compose.yaml ;;
	*) return 1 ;;
	esac
}

# The marker pass, in perl for the same reason as the anchors below. One
# program does both jobs so the check cannot drift from what is applied:
#   MARKERS_MODE=check  reports malformed markers, writes nothing
#   MARKERS_MODE=strip  also removes what MARKERS_REMOVE names, and prints the
#                       name of every file it changed
# Every file is checked before any is written, so a malformed marker in one
# file leaves all of them untouched.
MARKERS_PROGRAM=$(
	cat <<'PERL'
use strict;
use warnings FATAL => 'all';

my %known  = map { $_ => 1 } split /,/, $ENV{MARKERS_KNOWN};
my %remove = map { $_ => 1 } split /,/, $ENV{MARKERS_REMOVE};
my $strip  = $ENV{MARKERS_MODE} eq 'strip';
my $bad    = 0;
my %out;

for my $file (@ARGV) {
	open my $in, '<', $file or die "reading $file: $!\n";
	my (@kept, @open);
	my ($skip, $changed) = (0, 0);
	while (my $line = <$in>) {
		my $n = $.;
		if ($line =~ m{(?://|\#|<!--)\s*module:([a-z][a-z,]*)(?:\s+(begin|end)\b)?}) {
			my ($names, $kind) = ($1, $2 // 'line');
			my @names = split /,/, $names;
			for (grep { !$known{$_} } @names) {
				warn "$file:$n: unknown module \"$_\"\n";
				$bad = 1;
			}
			my $named = grep { $remove{$_} } @names;
			if ($kind eq 'begin') {
				push @open, [$names, $n];
				$skip++ if $skip || $named;
			} elsif ($kind eq 'end') {
				my $o = pop @open;
				if (!$o) {
					warn "$file:$n: \"module:$names end\" has no begin\n";
					$bad = 1;
				} elsif ($o->[0] ne $names) {
					warn "$file:$n: \"module:$names end\" closes \"module:$o->[0] begin\" from line $o->[1]\n";
					$bad = 1;
				}
				if ($skip) {
					$skip--;
					$changed = 1;
					next;
				}
			} elsif ($named) {
				$changed = 1;
				next;
			}
		}
		if ($skip) {
			$changed = 1;
			next;
		}
		push @kept, $line;
	}
	close $in;
	for my $o (@open) {
		warn "$file:$o->[1]: \"module:$o->[0] begin\" is never closed\n";
		$bad = 1;
	}
	$out{$file} = join '', @kept if $changed;
}

exit 1 if $bad;
exit 0 unless $strip;

for my $file (sort keys %out) {
	# A removed block leaves the blank lines on either side of it adjacent.
	(my $text = $out{$file}) =~ s/\n{3,}/\n\n/g;
	open my $o, '>', $file or die "writing $file: $!\n";
	print {$o} $text or die "writing $file: $!\n";
	close $o or die "writing $file: $!\n";
	print "$file\n";
}
PERL
)

# Runs the marker program over every tracked file that carries a marker.
# setup.sh is excluded because it describes the syntax, and git grep because
# only tracked files are ever touched. With no marked files, perl runs with no
# arguments and does nothing; the program never reads stdin.
markers() {
	{ git grep -lzE '(//|#|<!--)[[:space:]]*module:[a-z]' -- . ':!setup.sh' || true; } |
		MARKERS_MODE=$1 MARKERS_REMOVE=${2:-} MARKERS_KNOWN=$KNOWN_MODULES \
			xargs -0 perl -e "$MARKERS_PROGRAM"
}

# A module is present when any of its paths has a tracked file or any marker
# names it. Removal deletes both, so a module with neither is already gone —
# and one with only one of them is half-removed, which --check reports.
# path_state prints all, some or none: how many of a module's paths exist.
# On disk, not in the index: removal deletes with rm rather than git rm, so the
# index still lists a removed module's files until the fork commits.
path_state() {
	_found=0
	_missing=0
	for p in $(module_paths "$1"); do
		if [ -e "$p" ]; then _found=1; else _missing=1; fi
	done
	case $_found$_missing in
	10) echo all ;;
	01) echo none ;;
	*) echo some ;;
	esac
}
has_paths() { [ "$(path_state "$1")" != none ]; }
has_markers() {
	git grep -qE "(//|#|<!--)[[:space:]]*module:([a-z]+,)*$1(,[a-z]+)*([^a-z,]|\$)" -- . ':!setup.sh'
}
present() { has_paths "$1" || has_markers "$1"; }

list_present() {
	for m in $(printf '%s' "$KNOWN_MODULES" | tr ',' ' '); do
		if present "$m"; then printf '%s\n' "$m"; fi
	done
}

if [ "$ACTION" = modules ]; then
	list_present
	exit 0
fi

# Resolve --without into the modules actually still here, before anything is
# written. An unknown name is an error, not a skip: a typo'd --without that
# quietly removed nothing would look exactly like success.
REMOVE=''
for m in $(printf '%s' "$WITHOUT" | tr ',' ' '); do
	case ",$KNOWN_MODULES," in
	*",$m,"*) ;;
	*) die "unknown module \"$m\" — known modules: $KNOWN_MODULES" ;;
	esac
	if present "$m"; then
		REMOVE="${REMOVE:+$REMOVE,}$m"
	else
		printf '%b\n' "${GREEN}✓ already without $m$OFF"
	fi
done

# ---------------------------------------------------------------------------
# Rename

# Read the current path from go.mod rather than hardcoding it. That is what
# makes the script idempotent, and reading the file directly rather than asking
# `go list -m` means the check works before any Go toolchain is resolved.
OLD_MODULE=$(awk '$1 == "module" { print $2; exit }' go.mod)
[ -n "$OLD_MODULE" ] || die "could not read the module path from go.mod"

RENAME=0
if [ -z "$NEW_MODULE" ]; then
	# --check, or --without on its own: substituting the current name for
	# itself exercises the real patterns without a target module.
	NEW_MODULE=$OLD_MODULE
else
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
	else
		RENAME=1
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
		perl -Mwarnings=FATAL,inplace -pi -e "s/$_pattern/$_replacement/" "$_file" ||
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

markers check || die "module markers are malformed (see above), so nothing was written"

# A module must be wholly here or wholly gone. Its package without markers means
# wiring was added unmarked, which --without would leave behind, dangling; its
# markers without the package means a removal was finished by hand, badly.
for m in $(printf '%s' "$KNOWN_MODULES" | tr ',' ' '); do
	state=$(path_state "$m")
	if [ "$state" = some ]; then
		FAILED="$FAILED\n  module $m — only some of its paths exist: $(module_paths "$m")"
	elif [ "$state" = all ] && ! has_markers "$m"; then
		FAILED="$FAILED\n  module $m — its files are here but nothing is marked module:$m"
	elif [ "$state" = none ] && has_markers "$m"; then
		FAILED="$FAILED\n  module $m — markers name it but its files are gone"
	fi
done
[ -z "$FAILED" ] || {
	printf '%b%s%b' "$RED" "setup.sh: modules are half-present, so nothing was written:" "$OFF" >&2
	printf '%b\n' "$FAILED" >&2
	die "mark or remove what is left by hand, then re-run"
}

if [ "$ACTION" = check ]; then
	printf '%b\n' "${GREEN}✓ setup.sh anchors and module markers match$OFF"
	exit 0
fi

[ "$RENAME" = 1 ] || [ -n "$REMOVE" ] || exit 0

# Checked before the first write, so a missing toolchain leaves the tree
# untouched rather than changed and unverified.
command -v go >/dev/null 2>&1 || die "Go is not installed — install it and run setup.sh again; nothing was written"

# Every failure past this point says `git restore .` undoes it. That is only
# true, and only safe to follow, when the tracked tree held nothing but the
# template: run on uncommitted edits, it would discard them along with the rename.
# Untracked files are never written, so they do not count.
[ -z "$(git status --porcelain --untracked-files=no)" ] ||
	die "tracked files have uncommitted changes — commit or stash them first; nothing was written"

# A module's directory is deleted by its tracked files, so an untracked file
# inside it would survive, orphaned, and likely still import the dependency
# being removed. Refuse rather than leave that behind or delete something git
# cannot give back.
for m in $(printf '%s' "$REMOVE" | tr ',' ' '); do
	# shellcheck disable=SC2046
	stray=$(git ls-files --others --exclude-standard -- $(module_paths "$m"))
	[ -z "$stray" ] || die "untracked files inside module $m — move or commit them first; nothing was written:
$stray"
done

UNDO="the changes are left in place; \`git restore .\` undoes them"

if [ "$RENAME" = 1 ]; then
	# The module path is unambiguous enough to replace everywhere it appears,
	# which is the one substitution that does not need an anchor. git grep rather
	# than grep -r: an untracked note or local config that mentions the path is
	# not the template's to rewrite, and it skips binaries, .git and ignored
	# build output.
	git grep -qF "$OLD_MODULE" || die "no references to $OLD_MODULE found — is this a clone of the template?"
	REWRITTEN=$(git grep -lF "$OLD_MODULE" | wc -l | tr -d ' ')

	# FATAL,inplace: by default perl -pi only warns when it cannot write a file
	# and exits 0, so the skip would be silent. Fatal, it exits non-zero and xargs
	# does too. That matters most for files go build never compiles — a
	# _test.go, a doc.
	# shellcheck disable=SC2016
	git grep -lzF "$OLD_MODULE" | xargs -0 perl -Mwarnings=FATAL,inplace -pi -e 's{\Q$ENV{OLD_MODULE}\E}{$ENV{NEW_MODULE}}g' ||
		die "rewriting the module path failed — $UNDO"

	MODE=apply
	anchors

	# Recheck that every write landed. The fatal warning above catches a write
	# perl could not make; this catches one that was made and did not say what it
	# should. Before either existed, an unwritable file was skipped silently and
	# the script reported a complete rename. Running the check pass with the new
	# name as the old one asserts each anchor now carries it; go.mod stands in for
	# the module rewrite, and go build checks the imports.
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
fi

if [ -n "$REMOVE" ]; then
	# Tracked files only, with plain rm rather than git rm: the index keeps them,
	# so `git restore .` brings them back like every other change here.
	for m in $(printf '%s' "$REMOVE" | tr ',' ' '); do
		# shellcheck disable=SC2046
		git ls-files -z -- $(module_paths "$m") | xargs -0 rm -f -- ||
			die "deleting module $m failed — $UNDO"
		for p in $(module_paths "$m"); do
			[ ! -d "$p" ] || find "$p" -depth -type d -empty -delete
		done
	done

	STRIPPED=$(markers strip "$REMOVE") || die "removing module markers failed — $UNDO"

	# Removing a struct field or a case changes gofmt's alignment of what is
	# left, so the files that changed are formatted, or the fork's first
	# `make verify` would fail on gofmt.
	# shellcheck disable=SC2086 # the paths come from git and contain no spaces
	gofmt_targets=$(printf '%s\n' $STRIPPED | grep '\.go$' || true)
	if [ -n "$gofmt_targets" ]; then
		# shellcheck disable=SC2086
		gofmt -w $gofmt_targets || die "formatting failed — $UNDO"
	fi

	# Recheck that every removal landed: no path of a removed module still has a
	# file, and no marker still names one.
	for m in $(printf '%s' "$REMOVE" | tr ',' ' '); do
		! present "$m" || die "module $m is still partly present after removal — $UNDO"
	done

	# The removed packages were the only importers of their dependencies.
	go mod tidy || die "go mod tidy failed — $UNDO"
fi

# A failed build is deliberately not rolled back. The compile error points at
# files in the changed tree, which is where it has to be read, and every write
# above went to a tracked file, so git already is the undo.
go build ./... || die "the tree does not compile — see the error above; $UNDO"
go vet -tags integration ./... || die "go vet failed — see the error above; $UNDO"

if [ "$RENAME" = 1 ]; then
	printf '%b\n' "${GREEN}✓ $OLD_MODULE → $NEW_MODULE$OFF"
	printf '%b\n' "${DIM}  service name: $NEW_NAME$OFF"
	printf '%b\n' "${DIM}  rewritten:    $REWRITTEN files, plus 5 name references$OFF"
fi
if [ -n "$REMOVE" ]; then
	printf '%b\n' "${GREEN}✓ without $(printf '%s' "$REMOVE" | sed 's/,/, /g')$OFF"
	left=$(list_present | tr '\n' ' ')
	printf '%b\n' "${DIM}  still here:   ${left:-none}$OFF"
fi
printf '%b\n' "${DIM}  go build, go vet: ok$OFF"
printf '\n'
printf '%s\n' "Next:"
if [ "$RENAME" = 1 ]; then
	printf '%s\n' "  git remote set-url origin git@github.com:you/$NEW_NAME.git   # still points at the template"
fi
printf '%s\n' "  make verify"
printf '%s\n' "  git commit -am 'Set up from go-service-starter'"
