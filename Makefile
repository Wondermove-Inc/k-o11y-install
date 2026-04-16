# K-O11y Install

.PHONY: help build-db build-tls

## Build Go CLI tools
build-db:
	cd cmd/k-o11y-db && go build -o k-o11y-db .

build-tls:
	cd cmd/k-o11y-tls && go build -o k-o11y-tls .

## Help
help:
	@echo "K-O11y Install"
	@echo ""
	@echo "Usage:"
	@echo "  make build-db   - Build ClickHouse/Keeper installer CLI"
	@echo "  make build-tls  - Build TLS certificate setup CLI"
	@echo ""
	@echo "See README.md for full installation guide."
