.PHONY: lint test build web site-test

lint:
	golangci-lint run ./...

test:
	go test -race ./...

build:
	@if [ -d cmd/homed ]; then go build -o bin/homed ./cmd/homed; else echo "cmd/homed not present yet, skipping"; fi

web:
	@if [ -d web ]; then npm --prefix web ci && npm --prefix web run build; else echo "web/ not present yet, skipping"; fi

site-test:
	@if [ -d site ]; then node --test "site/core/*.test.mjs"; else echo "site/ not present yet, skipping"; fi
