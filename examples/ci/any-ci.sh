#!/bin/sh
# One pacto ci step for any CI system: Buildkite, CircleCI, Azure Pipelines,
# Jenkins or a plain shell. Run it with pacto on PATH and the repository
# checked out; pass the root and any pacto ci flag as arguments.
#
#   sh examples/ci/any-ci.sh . --registry ghcr.io/acme
#
# On a pull request it plans; on the merged default branch set
# PACTO_PUBLISH=true and it publishes every changed service.
#
# The exit status is pacto ci's own:
#   plan     0 clear, 1 blocked, 2 unverified
#   publish  0 every changed service published, 1 otherwise
set -eu

if [ "${PACTO_PUBLISH:-false}" = "true" ]; then
  exec pacto ci --publish "$@"
fi
exec pacto ci "$@"
