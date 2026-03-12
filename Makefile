BINARY ?= fish-container

.PHONY: help build test run clean

help:
	@echo "Targets:"
	@echo "  build  - build runtime binary"
	@echo "  test   - run go test"
	@echo "  run    - run CLI help"
	@echo "  clean  - remove local build artifacts"

build:
	go build -o bin/$(BINARY) ./cmd/fish-container

test:
	go test ./...

run:
	go run ./cmd/fish-container

clean:
	rm -rf bin
