BINARY ?= fish-container

.PHONY: help build test test-linux-e2e run clean

help:
	@echo "Targets:"
	@echo "  build  - build runtime binary"
	@echo "  test   - run go test"
	@echo "  test-linux-e2e - run privileged Linux end-to-end tests"
	@echo "  run    - run CLI help"
	@echo "  clean  - remove local build artifacts"

build:
	go build -o bin/$(BINARY) ./cmd/fish-container

test:
	go test ./...

test-linux-e2e: build
	sudo -E ./hack/e2e-linux.sh ./bin/$(BINARY)

run:
	go run ./cmd/fish-container

clean:
	rm -rf bin
