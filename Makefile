.PHONY: build test vet

build:
	go build ./...

vet:
	go vet ./...

test:
	go test -tags testing ./...
