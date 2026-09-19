.PHONY: guest check build

GUEST ?= dist/pysolate.wasm

build:
	go build ./...

guest:
	bash build-guest.sh

check:
	test -f "$(GUEST)"
	PYSOLATE_GUEST="$(abspath $(GUEST))" go test ./... -count=1
	PYTHONPATH=guest python3 -m unittest discover -s guest
	go vet ./...
