IMAGE ?= secret-protector:local

.PHONY: build docker-build fmt test test-race tidy vet verify

build:
	mkdir -p bin
	go build -o bin/secret-protector ./cmd/secret-protector

docker-build:
	docker build --tag $(IMAGE) .

fmt:
	go fmt ./...

test:
	go test ./...

test-race:
	go test -race ./...

tidy:
	go mod tidy

vet:
	go vet ./...

verify: fmt test vet build
