#!/usr/bin/env bash
# Checks that CHANGELOG.md is ready for a release, and prints the notes
# for that release.
#
# The first level 2 heading must be "## Unreleased". The second must be
# "## <version> - <YYYY-MM-DD>", with a real date. The section under the
# second heading must have some text. The script writes that text to the
# output file, or to standard output if there is no output file.
#
# Usage: release-notes.sh <version> [<output file>]
set -euo pipefail

if (($# < 1 || $# > 2)); then
	echo "usage: $0 <version> [<output file>]" >&2
	exit 2
fi
version=$1
out=${2:-}
changelog=CHANGELOG.md

# The ::error:: prefix makes GitHub Actions show the message as an error.
fail() {
	echo "::error file=$changelog::$*"
	exit 1
}

[[ -f $changelog ]] || fail "$changelog does not exist."

# Each entry is "<line number>:<heading>".
headings=()
while IFS= read -r line; do
	headings+=("$line")
done < <(grep -n '^## ' "$changelog" || true)

((${#headings[@]} >= 2)) ||
	fail "$changelog must have a '## Unreleased' section, then a '## $version - YYYY-MM-DD' section."

first=${headings[0]#*:}
second=${headings[1]#*:}

[[ $first == "## Unreleased" ]] ||
	fail "The first section must be '## Unreleased', above the $version section. It is '$first'."

heading_re='^## (.+) - ([0-9]{4}-[0-9]{2}-[0-9]{2})$'
if [[ ! $second =~ $heading_re ]] || [[ ${BASH_REMATCH[1]} != "$version" ]]; then
	fail "The section after '## Unreleased' must be '## $version - YYYY-MM-DD'. It is '$second'."
fi
date=${BASH_REMATCH[2]}

# GNU date takes -d, and BSD date takes -j -f. Both print a different date,
# or fail, for a date that does not exist, such as 2026-02-30.
parsed=$(date -d "$date" +%F 2>/dev/null || date -j -f %F "$date" +%F 2>/dev/null || true)
[[ $parsed == "$date" ]] || fail "The release date $date is not a valid date."

start=$((${headings[1]%%:*} + 1))
if ((${#headings[@]} > 2)); then
	end=$((${headings[2]%%:*} - 1))
else
	end=$(wc -l <"$changelog")
fi
notes=$(sed -n "${start},${end}p" "$changelog")

[[ $notes =~ [^[:space:]] ]] || fail "The $version section has no text."

if [[ -n $out ]]; then
	printf '%s\n' "$notes" >"$out"
else
	printf '%s\n' "$notes"
fi
