.PHONY: test generate lint

# Python venv setup: see README (Shazam sidecar).
test:
	go test ./...
	cd shazam && .venv/bin/python -m pytest -q
	cd web && npx vitest run

generate:
	go tool sqlc generate

lint:
	go vet ./...
