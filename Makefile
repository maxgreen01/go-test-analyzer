# Makefile for go-test-analyzer cross-compilation

BINARY_NAME := go-test-analyzer
BUILD_DIR := ./build
MAIN_PACKAGE_PATH := ./cmd/analyzer

# Version tag of the current commit, otherwise TAG-N-COMMIT (where N is the number of commits since the last tag).
# Appends -dirty if there are uncommitted changes.
VERSION := $(shell git describe --tags --always --dirty)
BINARY_PATH := $(BUILD_DIR)/$(BINARY_NAME)-$(VERSION)
LDFLAGS := --ldflags "-X main.version=$(VERSION)"

GO_CMD := go
GO_OS := $(shell go env GOOS)

.PHONY: all clean cross-compile help


all:               ## Build the binary for the current platform
	mkdir -p $(BUILD_DIR)
ifeq ($(GO_OS), windows)
	$(GO_CMD) build -v -o $(BINARY_PATH).exe $(LDFLAGS) $(MAIN_PACKAGE_PATH)
else
	$(GO_CMD) build -v -o $(BINARY_PATH) $(LDFLAGS) $(MAIN_PACKAGE_PATH)
endif


clean:             ## Remove build artifacts
	$(GO_CMD) clean ./...
	rm $(BUILD_DIR)/*


cross-compile:     ## Cross-compile for multiple platforms
cross-compile: windows-amd64 linux-amd64 linux-arm64 darwin-amd64 darwin-arm64
.PHONY: windows-amd64 linux-amd64 linux-arm64 darwin-amd64 darwin-arm64

windows-amd64:
	GOOS=windows GOARCH=amd64 $(GO_CMD) build -o $(BINARY_PATH)-windows-amd64.exe $(LDFLAGS) $(MAIN_PACKAGE_PATH)

linux-amd64:
	GOOS=linux GOARCH=amd64 $(GO_CMD) build -o $(BINARY_PATH)-linux-amd64 $(LDFLAGS) $(MAIN_PACKAGE_PATH)

linux-arm64:
	GOOS=linux GOARCH=arm64 $(GO_CMD) build -o $(BINARY_PATH)-linux-arm64 $(LDFLAGS) $(MAIN_PACKAGE_PATH)

darwin-amd64:
	GOOS=darwin GOARCH=amd64 $(GO_CMD) build -o $(BINARY_PATH)-darwin-amd64 $(LDFLAGS) $(MAIN_PACKAGE_PATH)

darwin-arm64:
	GOOS=darwin GOARCH=arm64 $(GO_CMD) build -o $(BINARY_PATH)-darwin-arm64 $(LDFLAGS) $(MAIN_PACKAGE_PATH)


help:              ## Show this help menu
	@echo "Usage: make [target]"
	@echo
	@echo "Targets:"
	@sed -ne '/@sed/!s/## //p' $(MAKEFILE_LIST)
