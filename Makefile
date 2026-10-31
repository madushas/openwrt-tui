.PHONY: build test vet fmt check vendor-charm release

build:
	go build -o bin/owrt-tui ./cmd/owrt-tui

test:
	go test -race -count=1 ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

check: vet test
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)

# make release VERSION=v0.1.0  ->  dist/*.tar.gz, *.zip and SHA256SUMS
release:
	scripts/release.sh $(VERSION)
