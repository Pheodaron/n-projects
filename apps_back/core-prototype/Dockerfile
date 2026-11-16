# syntax=docker/dockerfile:1
FROM golang:1.27.3-alpine AS builder
WORKDIR /src
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /core-prototype ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /core-prototype /core-prototype
EXPOSE 8090
ENTRYPOINT ["/core-prototype"]
