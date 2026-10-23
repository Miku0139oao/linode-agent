BIN := bin/linode-agent
PKG := ./cmd/linode-agent

.PHONY: build test vet fmt check run clean

build:
	@mkdir -p bin
	go build -trimpath -o $(BIN) $(PKG)
	@echo "built $(BIN)"

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w $$(find . -name '*.go' -not -path './bin/*')

check: vet test
	@test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './bin/*'))" || (echo "gofmt needed:" && gofmt -l $$(find . -name '*.go' -not -path './bin/*') && exit 1)

run:
	go run $(PKG)

clean:
	rm -rf bin
