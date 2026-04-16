BIN_NAME := k-o11y-db
BUILD_DIR := dist
VERSION ?= dev
LDFLAGS := -s -w -X github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-db/internal/agent.Version=$(VERSION)

.PHONY: build build-all clean test

build:
	go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BIN_NAME) .

build-all:
	GOOS=linux  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/linux-amd64/$(BIN_NAME) .
	GOOS=linux  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/linux-arm64/$(BIN_NAME) .
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/darwin-amd64/$(BIN_NAME) .
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/darwin-arm64/$(BIN_NAME) .

clean:
	rm -rf $(BUILD_DIR)

test:
	go test ./...
