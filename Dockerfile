### stage: get nats exporter
FROM curlimages/curl:latest as metrics
ARG PROMETHEUS_NATS_VERSION=v0.17.2

WORKDIR /metrics/
USER root
RUN mkdir -p /metrics/
RUN curl -o nats-exporter.tar.gz -L https://github.com/nats-io/prometheus-nats-exporter/releases/download/${PROMETHEUS_NATS_VERSION}/prometheus-nats-exporter-${PROMETHEUS_NATS_VERSION}-linux-amd64.tar.gz
RUN tar zxvf nats-exporter.tar.gz
RUN mv prometheus-nats-exporter*/prometheus-nats-exporter ./

### stage: build flyutil
FROM golang:1 as flyutil
ARG VERSION

WORKDIR /go/src/github.com/fly-apps/nats-cluster
COPY go.mod ./
COPY go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -v -o /fly/bin/start ./cmd/start

# stage: final image
FROM nats:2.11-scratch as nats-server

FROM debian:bookworm-slim

COPY --from=nats-server /nats-server /usr/local/bin/
COPY --from=metrics /metrics/prometheus-nats-exporter /usr/local/bin/nats-exporter
COPY --from=flyutil /fly/bin/start /usr/local/bin/

CMD ["start"]
