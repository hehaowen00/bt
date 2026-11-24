# `make help` lists the targets.
UNAME := $(shell uname -s)
OS := $(if $(filter Darwin,$(UNAME)),macos,$(if $(filter Linux,$(UNAME)),linux,windows))
# only-on: fail fast when a per-OS target is run on the wrong OS.
only-on = @[ "$(OS)" = "$(1)" ] || { echo "$@ needs $(1) (this is $(OS))"; exit 1; }

EXE := $(if $(filter windows,$(OS)),.exe)
BIN := bt$(EXE)

# `make install` is user-local and needs no root. System-wide: sudo make install PREFIX=/usr/local
PREFIX ?= $(HOME)/.local
DESTDIR ?=

.DEFAULT_GOAL := help
.PHONY: help build run test lint lint-fix fmt vet install uninstall clean \
        macos-install linux-install windows-install

help:
	@awk -F':.*## ' '/^## /{printf "\n%s\n", substr($$0,4); next} /^[a-z0-9-]+:.*## /{printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo

## Build
build: ## build ./bt
	go build -o $(BIN)
clean: ## delete the binary
	rm -f $(BIN)

## Develop
run: build ## run it; ARGS="file.torrent" or ARGS="'magnet:?…'" adds them
	./$(BIN) $(ARGS)

## Check
test: ## run the tests, with the race detector
	go test -race ./...
vet: ## go vet
	go vet ./...
lint: ## layout: one-line closures, crowded literals, blank lines, header clutter, long lines
	go run ./tools/funclit
lint-fix: ## rewrite what make lint reports, then gofmt
	go run ./tools/funclit -fix
fmt: ## gofmt everything
	gofmt -w .

## Install
# mkdir rather than install -D: macOS's install has no -D
install: build ## build for this OS and install onto PATH in PREFIX/bin
	mkdir -p "$(DESTDIR)$(PREFIX)/bin" && install -m755 $(BIN) "$(DESTDIR)$(PREFIX)/bin/$(BIN)"
	@echo "installed $(BIN) in $(PREFIX)/bin"
	@case ":$$PATH:" in *":$(PREFIX)/bin:"*) ;; *) echo "note: $(PREFIX)/bin is not on PATH";; esac
macos-install: ## install, checking this is macOS
	$(call only-on,macos)
	$(MAKE) install
linux-install: ## install, checking this is Linux
	$(call only-on,linux)
	$(MAKE) install
windows-install: ## install, checking this is Windows (from Git Bash or MSYS2)
	$(call only-on,windows)
	$(MAKE) install
uninstall: ## remove the binary
	rm -f "$(DESTDIR)$(PREFIX)/bin/$(BIN)"
