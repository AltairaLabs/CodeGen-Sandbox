.PHONY: build test lint fmt tidy docker-build docker-run docker-clean

IMAGE ?= codegen-sandbox:dev

build:
	go build -o bin/sandbox ./cmd/sandbox

test:
	go test ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .
	goimports -w .

tidy:
	go mod tidy

docker-build:
	docker build -t $(IMAGE) .

docker-run:
	mkdir -p /tmp/codegen-sandbox-workspace
	docker run --rm -it \
		-p 8080:8080 \
		-v /tmp/codegen-sandbox-workspace:/workspace \
		$(IMAGE)

docker-clean:
	docker rmi $(IMAGE) 2>/dev/null || true
