# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build

ARG GOPROXY=https://proxy.golang.org,direct

RUN apk add --no-cache ca-certificates

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    GOPROXY="${GOPROXY}" go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOPROXY="${GOPROXY}" go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/secret-protector \
    ./cmd/secret-protector

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=65532:65532 /out/secret-protector /usr/local/bin/secret-protector

WORKDIR /config
USER 65532:65532

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/secret-protector"]
CMD ["serve"]
