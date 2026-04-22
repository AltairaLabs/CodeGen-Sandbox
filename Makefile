.PHONY: build test lint fmt tidy

build:
	go build -o bin/sandbox ./cmd/sandbox

test:
	go test ./...

lint:
	golangci-lint run ./cmd/...

fmt:
	gofmt -w .
	goimports -w .

tidy:
	go mod tidy
