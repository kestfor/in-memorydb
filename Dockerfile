FROM golang:1.26-alpine AS builder

RUN apk add --no-cache curl

ARG GRPC_HEALTH_PROBE_VERSION=v0.4.47
ARG GRPC_HEALTH_PROBE_SHA256=3d622aa31d1e6628f9a5b67c3cc367233d3eebe13c87e663079c2102da2b9030
RUN curl -L \
  "https://github.com/grpc-ecosystem/grpc-health-probe/releases/download/${GRPC_HEALTH_PROBE_VERSION}/grpc_health_probe-linux-amd64" \
  -o /bin/grpc_health_probe && \
  echo "${GRPC_HEALTH_PROBE_SHA256}  /bin/grpc_health_probe" | sha256sum -c - && \
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