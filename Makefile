.PHONY: build test check web-build web-dev

BINDIR ?= $(HOME)/go/bin

build:
	mkdir -p "$(BINDIR)"
	go build -trimpath -o "$(BINDIR)/paw" ./cmd/paw

test:
	go test ./... -count=1

check:
	go vet ./...
	go build ./...

web-build:
	./scripts/build-web.sh

web-dev:
	./scripts/dev-web.sh
