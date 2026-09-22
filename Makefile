.PHONY: bootstrap guest repack verify-artifact artifact-bundle artifact-install workloads replay-check check build

GUEST ?= dist/pysolate.wasm
MANIFEST ?= dist/pysolate.manifest.json
ARTIFACT_BUNDLE ?= .artifacts-private/pysolate-agent-core.tar.gz
BUNDLE ?=

build:
	go build ./...

bootstrap:
	python3 tools/setup-build-inputs.py
	python3 tools/setup-numpy.py

guest:
	PYSOLATE_RELINK=1 bash build-guest.sh

repack:
	PYSOLATE_RELINK=0 bash build-guest.sh

verify-artifact:
	python3 tools/artifact_bundle.py verify --artifact "$(GUEST)" --manifest "$(MANIFEST)"

artifact-bundle: verify-artifact
	python3 tools/artifact_bundle.py bundle --artifact "$(GUEST)" --manifest "$(MANIFEST)" --output "$(ARTIFACT_BUNDLE)"

artifact-install:
	@test -n "$(BUNDLE)" || { echo 'set BUNDLE to a local .tar.gz path or HTTPS URL' >&2; exit 2; }
	python3 tools/artifact_bundle.py install --source "$(BUNDLE)" --dist "$(dir $(GUEST))"

workloads: verify-artifact
	PYSOLATE_GUEST="$(abspath $(GUEST))" ./demos/14-agent-workloads.sh

replay-check: verify-artifact
	PYSOLATE_GUEST="$(abspath $(GUEST))" ./demos/15-deterministic-replay.sh

check: verify-artifact
	PYSOLATE_GUEST="$(abspath $(GUEST))" go test ./... -count=1
	PYTHONPATH=guest python3 -m unittest discover -s guest
	python3 -m unittest discover -s tools -p 'test_*.py'
	go vet ./...
