#!/usr/bin/env bash
set -euo pipefail

source "$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/common.sh"

banner "Host-owned workspace continuation across disposable Guests"
printf '%s\n' 'Observe: the first Guest writes progress; the second reads and extends it through the same workspace.'
printf '%s\n' 'This is file continuity, not RunRecorded replay.'
go run ./examples/workspace-continuation -guest "$GUEST" | pretty_json
