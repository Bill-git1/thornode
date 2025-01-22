
.PHONY: tools test unittest

all: unittest test

tools:
	go install ./cmd/tss-recovery
	go install ./cmd/tss-benchgen
	go install ./cmd/tss-benchsign

test:
	@CGO_ENABLED=1 go test --race ./...

unittest:
	@CGO_ENABLED=1 go test --race -v -coverprofile=coverage.out -timeout 15m ./...
	@go tool cover -func=coverage.out
