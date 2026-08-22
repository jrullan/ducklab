# The MCP server, containerized for registry health checks (Glama and the
# official registry's inspectors start the server and speak MCP over stdio).
# `ducklab mcp serve` needs the engine; the CLI auto-starts it (B-111), and
# in here loopback-only means container-only — same guarantee as on a host.
FROM golang:1.25-alpine AS build
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN LDFLAGS="-X github.com/jrullan/ducklab/internal/build.Version=$(git describe --tags --always 2>/dev/null || echo docker) -X github.com/jrullan/ducklab/internal/build.Commit=$(git rev-parse HEAD 2>/dev/null || echo unknown)" && \
    CGO_ENABLED=0 go build -ldflags "$LDFLAGS" -o /out/ducklab ./cmd/ducklab && \
    CGO_ENABLED=0 go build -ldflags "$LDFLAGS" -o /out/ducklab-engine ./cmd/ducklab-engine

FROM alpine:3.20
RUN apk add --no-cache git ca-certificates
COPY --from=build /out/ducklab /out/ducklab-engine /usr/local/bin/
# The engine refuses to run as root's HOME-less ghost; give it a home.
RUN adduser -D duck
USER duck
ENV HOME=/home/duck
ENTRYPOINT ["ducklab", "mcp", "serve"]
