### stage: get nats exporter
FROM curlimages/curl:latest AS metrics
ARG PROMETHEUS_NATS_VERSION=v0.17.2
ARG TARGETPLATFORM

WORKDIR /metrics/
USER root
RUN mkdir -p /metrics/
RUN if [ "$TARGETPLATFORM" = "linux/arm64" ]; then \
        ARCH="arm64"; \
    elif [ "$TARGETPLATFORM" = "linux/amd64" ]; then \
        ARCH="x86_64"; \
    else \
        echo "Unsupported platform: $TARGETPLATFORM"; \
        exit 1; \
    fi; \
    echo "Download https://github.com/nats-io/prometheus-nats-exporter/releases/download/${PROMETHEUS_NATS_VERSION}/prometheus-nats-exporter-${PROMETHEUS_NATS_VERSION}-linux-${ARCH}.tar.gz"; \
    curl -o nats-exporter.tar.gz -L "https://github.com/nats-io/prometheus-nats-exporter/releases/download/${PROMETHEUS_NATS_VERSION}/prometheus-nats-exporter-${PROMETHEUS_NATS_VERSION}-linux-${ARCH}.tar.gz"
RUN tar zxvf nats-exporter.tar.gz

### stage: build flyutil
FROM golang:1 AS flyutil
ARG VERSION

WORKDIR /go/src/github.com/connyay/nats-cluster
COPY go.mod ./
COPY go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod  CGO_ENABLED=0 GOOS=linux go build -v -o /fly/bin/start ./cmd/start

# stage: final image
FROM nats:2.11-scratch AS nats-server

FROM debian:bookworm-slim

COPY --from=nats-server /nats-server /usr/local/bin/
COPY --from=metrics /metrics/prometheus-nats-exporter /usr/local/bin/nats-exporter
COPY --from=flyutil /fly/bin/start /usr/local/bin/

# TCP Clients
EXPOSE 4222
# TCP Clustering (remapped by fly)
EXPOSE 7221
# HTTP
EXPOSE 8222

CMD ["start"]
