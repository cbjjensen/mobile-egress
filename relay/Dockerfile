# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY relay ./relay
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/relay ./relay/cmd/relay

FROM alpine:3.22

RUN mkdir -p /var/lib/mobile-egress
COPY --from=build /out/relay /usr/local/bin/relay

EXPOSE 8443/tcp
VOLUME ["/var/lib/mobile-egress"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget --no-check-certificate -q -O - https://127.0.0.1:8443/healthz | grep -q '"readiness":true' || exit 1

ENTRYPOINT ["/usr/local/bin/relay"]
CMD ["serve", "--state-dir", "/var/lib/mobile-egress/state", "--listen", "0.0.0.0:8443"]
