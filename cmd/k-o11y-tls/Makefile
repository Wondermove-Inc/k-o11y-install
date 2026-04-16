BIN_NAME := k-o11y-tls
BUILD_DIR := dist
VERSION ?= dev

.PHONY: build build-all clean test

build:
	go build -ldflags "-s -w -X main.version=$(VERSION)" -o $(BUILD_DIR)/$(BIN_NAME) .

build-all:
	GOOS=linux  GOARCH=amd64 go build -ldflags "-s -w" -o $(BUILD_DIR)/linux-amd64/$(BIN_NAME) .
	GOOS=linux  GOARCH=arm64 go build -ldflags "-s -w" -o $(BUILD_DIR)/linux-arm64/$(BIN_NAME) .
	GOOS=darwin GOARCH=amd64 go build -ldflags "-s -w" -o $(BUILD_DIR)/darwin-amd64/$(BIN_NAME) .
	GOOS=darwin GOARCH=arm64 go build -ldflags "-s -w" -o $(BUILD_DIR)/darwin-arm64/$(BIN_NAME) .

clean:
	rm -rf $(BUILD_DIR)

test:
	go test ./...
