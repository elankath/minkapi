#!/usr/bin/env zsh
set -eo pipefail

echoErr() { echo "$@" 1>&2; }

if [[ ! -d "$GOBIN"  || ! -d "$GOPATH"/bin ]]; then
  echoErr "GOBIN dir does not exist nor is GOPATH/bin exist"
fi

if [[ ! -f ./bin/minkapi ]]; then
  echoErr "minkapi has not yet been built - please invoke 'make build'"
fi

