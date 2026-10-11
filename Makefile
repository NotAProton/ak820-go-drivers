# Build, test, and install the ak820 CLI.

GO ?= go
BINARY := bin/ak820
PREFIX ?= ~/.local

.PHONY: build test vet fmt install clean

build: test
	$(GO) build -trimpath -ldflags="-s -w" -o $(BINARY) ./cmd/ak820

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

install: build
	install -m 0755 $(BINARY) $(PREFIX)/bin/ak820

clean:
	rm -rf bin
