#!/usr/bin/env bash
#
# Check this generator against a reference project that already tracks its
# generated output.
#
# The generator's -check mode re-renders everything and compares it to what is
# on disk, byte for byte, writing nothing. Pointing it at a project outside
# this repository is therefore a safe way to ask "does my change to the
# generator perturb anyone's committed output?" — a question the example golden
# alone cannot answer, since the example exercises far fewer definitions.
#
#   REF_CONFIG      required   a rosidl-gen.yaml belonging to that project
#
# When that config resolves interfaces through ${ROS_ROOT}/share, supply the
# tree to stand in for an installation — either a directory, or a git checkout
# to extract read-only:
#
#   ROS_SHARE       a directory of ament packages
#   ROS_SHARE_GIT   a git checkout to extract instead
#   ROS_SHARE_REF   which ref to extract (default: HEAD)
#
# Nothing is written outside a temporary directory, and the reference project
# is only ever read.

set -euo pipefail
cd "$(dirname "$0")/.."

: "${REF_CONFIG:?set REF_CONFIG to a rosidl-gen.yaml outside this repository}"
if [ ! -f "$REF_CONFIG" ]; then
	echo "verify: no such config: $REF_CONFIG" >&2
	exit 2
fi

scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT

if [ -n "${ROS_SHARE_GIT:-}" ]; then
	mkdir -p "$scratch/ros/share"
	git -C "$ROS_SHARE_GIT" archive "${ROS_SHARE_REF:-HEAD}" | tar -x -C "$scratch/ros/share"
	export ROS_ROOT="$scratch/ros"
elif [ -n "${ROS_SHARE:-}" ]; then
	if [ ! -d "$ROS_SHARE" ]; then
		echo "verify: no such directory: $ROS_SHARE" >&2
		exit 2
	fi
	mkdir -p "$scratch/ros"
	ln -s "$ROS_SHARE" "$scratch/ros/share"
	export ROS_ROOT="$scratch/ros"
fi

# gofmt output can shift between Go releases, and -check compares bytes. Run
# under the toolchain this module declares so a version difference on the
# machine cannot masquerade as generator drift.
declared_go="$(go list -m -f '{{.GoVersion}}')"
export GOTOOLCHAIN="go${declared_go}"

go run . -config "$REF_CONFIG" -check
