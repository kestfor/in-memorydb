FROM golang:1.25-alpine AS builder

RUN apk add --no-cache curl

RUN curl -L \
  https://github.com/grpc-ecosystem/grpc-health-probe/releases/latest/download/grpc_health_probe-linux-amd64 \
  -o /bin/grpc_health_probe && \
  chmod +x /bin/grpc_health_probe

WORKDIR /app
COPY ./go.mod ./go.sum ./
RUN go mod download
COPY .. .

RUN CGO_ENABLED=0 GOOS=linux go build -o /app/main ./cmd/grpc/

FROM scratch
COPY --from=builder /bin/grpc_health_probe /bin/grpc_health_probe
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/main /lume