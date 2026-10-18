#!/usr/bin/env bash
#
# check-version.sh BASE_REF
#
# Fails unless the VERSION file on the current checkout is a valid
# MAJOR.MINOR.PATCH version that is higher than the one on BASE_REF and
# has no matching v<version> tag yet.

set -euo pipefail

base_ref=${1:?usage: check-version.sh BASE_REF}

new=$(tr -d '[:space:]' < VERSION)
old=$(git show "$base_ref:VERSION" 2>/dev/null | tr -d '[:space:]' || true)
old=${old:-0.0.0}

if ! [[ $new =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "::error file=VERSION::VERSION '$new' is not in MAJOR.MINOR.PATCH format"
    exit 1
fi

if [[ $new == "$old" ]]; then
    echo "::error file=VERSION::VERSION is still $old. Raise it before merging into main."
    exit 1
fi

highest=$(printf '%s\n%s\n' "$old" "$new" | sort -V | tail -n1)
if [[ $highest != "$new" ]]; then
    echo "::error file=VERSION::VERSION $new is lower than $old on the base branch"
    exit 1
fi

if git rev-parse -q --verify "refs/tags/v$new" >/dev/null; then
    echo "::error file=VERSION::Tag v$new already exists"
    exit 1
fi

echo "VERSION $old -> $new"
