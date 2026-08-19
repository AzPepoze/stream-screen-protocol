FROM golang:1.26-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY tests/netem/cmd/relay ./tests/netem/cmd/relay

RUN CGO_ENABLED=0 go build -trimpath -o /out/netem-relay ./tests/netem/cmd/relay

FROM debian:bookworm-slim

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates iproute2 \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/netem-relay /usr/local/bin/netem-relay
COPY tests/netem/entrypoint.sh /usr/local/bin/netem-entrypoint
RUN chmod +x /usr/local/bin/netem-entrypoint

EXPOSE 5000/udp
ENTRYPOINT ["/usr/local/bin/netem-entrypoint"]
