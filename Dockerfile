# syntax=docker/dockerfile:1
FROM golang:1.27.3-alpine AS builder
WORKDIR /src
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build --mount=type=secret,id=proxy_ca,target=/etc/ssl/certs/ca-certificates.crt \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server && \
    CGO_ENABLED=0 GOBIN=/out go install -tags='no_clickhouse no_mssql no_mysql no_sqlite3 no_libsql no_vertica no_ydb' github.com/pressly/goose/v3/cmd/goose@v3.24.3

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /out/server /server
COPY --from=builder /out/goose /goose
COPY migrations/*.sql /migrations/
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/server"]
