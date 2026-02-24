.PHONY: build test lint

build:
	go build ./cmd/ottercamp

test:
	go test ./... -cover

lint:
	go vet ./...
