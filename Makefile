.PHONY: test generate lint

# TODO: python and web test suites are added in later tasks.
test:
	go test ./...

generate:
	go tool sqlc generate

lint:
	go vet ./...
