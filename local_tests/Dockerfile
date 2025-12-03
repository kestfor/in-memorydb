FROM golang:1.25-alpine AS builder

WORKDIR /app
COPY ./go.mod ./go.sum ./
RUN go mod download
COPY .. .

RUN CGO_ENABLED=0 GOOS=linux go build -o /app/main ./cmd/grpc/
RUN mkdir "/wal_data"

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/main /lume
COPY --from=builder /app/local_tests/configs /configs
COPY --from=builder /wal_data /wal_data