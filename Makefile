.PHONY: lint test vuln build web site-test e2e

lint:
	golangci-lint run ./...

test:
	go test -race ./...

# Known vulnerabilities reachable from the code (the go.mod toolchain's standard library and every module); CI runs it too.
vuln:
	go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...

# homed embeds the admin SPA (internal/web/dist), so the SPA is built first; without it /admin answers 503.
build: web
	@if [ -d cmd/homed ]; then go build -o bin/homed ./cmd/homed; else echo "cmd/homed not present yet, skipping"; fi

web:
	@if [ -d web ]; then npm --prefix web ci && npm --prefix web run build; else echo "web/ not present yet, skipping"; fi

site-test:
	@if [ -d site ]; then node --test "site/core/*.test.mjs"; else echo "site/ not present yet, skipping"; fi

# Playwright against a real homed (docs/TESTING.md): builds the SPA, then web/tests/real builds homed and the fake
# plugin into a temp dir and drives first run → passkey → tool → key → REST → revoke. Needs Chromium for Playwright
# once: npx --prefix web playwright install chromium.
e2e: web
	npm --prefix web run e2e:real
