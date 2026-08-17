IMAGE ?= secret-protector:local
GOPROXY ?= https://proxy.golang.org,direct

.PHONY: build docker-build fmt test test-race tidy vet verify

build:
	mkdir -p bin
	go build -o bin/secret-protector ./cmd/secret-protector

docker-build:
	docker build --build-arg "GOPROXY=$(GOPROXY)" --tag $(IMAGE) .

fmt:
	gofmt -w $$(rg --files -g '*.go')

test:
	go test ./...

test-race:
	go test -race ./...

tidy:
	go mod tidy

vet:
	go vet ./...

verify: fmt test vet build
