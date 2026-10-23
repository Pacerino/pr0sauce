.PHONY: test generate lint

# TODO: the web test suite is added in a later task.
# Python venv setup: see README (Shazam sidecar).
test:
	go test ./...
	cd shazam && .venv/bin/python -m pytest -q

generate:
	go tool sqlc generate

lint:
	go vet ./...
