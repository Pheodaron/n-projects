# syntax=docker/dockerfile:1
FROM golang:1.27.3-alpine AS builder
ENV GOWORK=off
WORKDIR /src
COPY . .
WORKDIR /src/apps_back/server
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /server ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /server /server
EXPOSE 8080
ENTRYPOINT ["/server"]
