#!/usr/bin/env sh
# bump-version.sh — bump the patch/minor/major component of VERSION in place.
# Usage: scripts/bump-version.sh [patch|minor|major]
# The bumped VERSION file is committed; the release workflow turns it into a
# release automatically on the next push to main.

set -eu

part="${1:-patch}"
v="$(cat VERSION)"
major="${v%%.*}"
rest="${v#*.}"
minor="${rest%%.*}"
patch="${rest#*.}"

case "$part" in
  patch) patch=$((patch + 1)) ;;
  minor) minor=$((minor + 1)); patch=0 ;;
  major) major=$((major + 1)); minor=0; patch=0 ;;
  *)
    echo "usage: bump-version.sh [patch|minor|major]" >&2
    exit 1
    ;;
esac

new="$major.$minor.$patch"
echo "$new" > VERSION
echo "VERSION: $v -> $new"
