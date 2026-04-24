.PHONY: build build-forward test test-integration lint fmt tidy \
        docker-build-tools docker-build-tools-node docker-build docker-run docker-clean \
        docker-build-python docker-build-node docker-build-rust

# The Go-language convenience image.
IMAGE ?= codegen-sandbox:dev
# The tools artifact image the convenience images COPY binaries from.
TOOLS_IMAGE ?= codegen-sandbox-tools:dev

build: build-forward
	go build -o bin/sandbox ./cmd/sandbox

build-forward:
	go build -o bin/sandbox-forward ./cmd/sandbox-forward

test:
	go test ./...

# Run integration tests that drive real external binaries (gopls,
# golangci-lint, go). Tests gated by `//go:build integration` skip at
# runtime when their binary isn't on PATH, so this target is safe to run
# on a machine that only has a subset of them installed.
test-integration:
	go test -tags=integration -race -count=1 -timeout=120s ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .
	goimports -w .

tidy:
	go mod tidy

# Build the minimal artifact image (scratch + /sandbox + /rg).
docker-build-tools:
	docker build -f Dockerfile.tools -t $(TOOLS_IMAGE) .

# Build the Node feature tools layer (scratch + /pnpm + /bun).
docker-build-tools-node:
	docker build -f Dockerfile.tools-node -t codegen-sandbox-tools-node:dev .

# Build the Go convenience image — requires docker-build-tools first (the
# main Dockerfile COPYs from codegen-sandbox-tools:dev).
docker-build: docker-build-tools
	docker build -t $(IMAGE) .

docker-run:
	mkdir -p /tmp/codegen-sandbox-workspace
	docker run --rm -it \
		-p 8080:8080 \
		-v /tmp/codegen-sandbox-workspace:/workspace \
		$(IMAGE)

# Per-language example convenience images — each requires the tools image.
docker-build-python: docker-build-tools
	docker build -f examples/Dockerfile.python -t codegen-sandbox-python:dev .

docker-build-node: docker-build-tools
	docker build -f examples/Dockerfile.node -t codegen-sandbox-node:dev .

docker-build-rust: docker-build-tools
	docker build -f examples/Dockerfile.rust -t codegen-sandbox-rust:dev .

docker-clean:
	docker rmi $(IMAGE) $(TOOLS_IMAGE) \
		codegen-sandbox-python:dev \
		codegen-sandbox-node:dev \
		codegen-sandbox-rust:dev 2>/dev/null || true
