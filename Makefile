SHELL := /bin/bash
.DEFAULT_GOAL := help

VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
GUI_TARGET ?= all    # linux | windows | all — restricts GUI cross-build toolchains
ARGS ?=              # extra flags, e.g. make run-cli ARGS=-config.json

.PHONY: help build cli gui test vet run-gui run-cli deb rpm msi dist clean

help: ## Show available targets
	@echo "T-Invest Snapshot — build and release"
	@echo ""
	@echo "  Targets:"
	@echo "    make [build]   Build all binaries (CLI + GUI, linux+windows) into dist/"
	@echo "    make cli       Build CLI binaries only (linux + windows)"
	@echo "    make gui       Build GUI binaries; GUI_TARGET=linux|windows|all (default all)"
	@echo "    make test      Run the full Go test suite"
	@echo "    make vet       go vet on all packages"
	@echo "    make run-cli   Launch CLI via 'go run' (ARGS='-flag ...')"
	@echo "    make run-gui   Launch GUI via 'go run'"
	@echo "    make deb       Build .deb package only (dpkg-deb + X11 headers required)"
	@echo "    make rpm       Build .rpm package only"
	@echo "    make msi       Build Windows MSI installer (wixl/msitools + mingw-w64; skipped if absent)"
	@echo "    make dist      Full release artifacts: deb, rpm, msi, tar.gz/zip + SHA256SUMS.txt"
	@echo "    make clean     Remove dist/"

build: cli gui ## Build all binaries (alias for 'cli' + 'gui')

cli: ## Cross-compile static CLI binaries
	bash scripts/build.sh

gui: ## Build Fyne GUI artifacts
	bash scripts/build-gui.sh $(GUI_TARGET)

test: ## Run the full Go test suite
	go test ./...

vet: ## Vet all packages
	go vet ./...

run-cli: ## Launch CLI via go run (ARGS for extra flags)
	go run ./cmd/snapshot $(ARGS)

run-gui: ## Launch GUI via go run
	go run ./cmd/gui $(ARGS)

deb: ## Build the .deb package only
	bash scripts/package.sh "$(VERSION)" deb

rpm: ## Build the .rpm package only
	bash scripts/package.sh "$(VERSION)" rpm

msi: ## Build the Windows MSI installer (skipped if toolchain absent)
	bash scripts/package.sh "$(VERSION)" msi

dist: ## Full release artifact set into dist/
	bash scripts/package.sh all

clean: ## Remove build output
	rm -rf dist
