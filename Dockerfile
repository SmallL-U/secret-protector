# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build

RUN apk add --no-cache ca-certificates

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/secret-protector \
    ./cmd/secret-protector

FROM alpine:3.24

RUN apk add --no-cache ca-certificates

COPY --from=build --chmod=0555 /out/secret-protector /usr/local/bin/secret-protector

WORKDIR /config

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/secret-protector"]
CMD ["--config", "/config/config.yml", "serve"]
