# syntax=docker/dockerfile:1
# Stampede: one binary for server, worker and CLI.
#   docker build -t ghcr.io/ivan825/stampede .
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/Ivan825/Stampede/internal/version.Version=${VERSION} -X github.com/Ivan825/Stampede/internal/version.Commit=${COMMIT} -X github.com/Ivan825/Stampede/internal/version.Date=${DATE}" \
      -o /out/stampede ./cmd/stampede && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/stampede /usr/local/bin/stampede
# Owned by the nonroot user so a named volume mounted here is writable
# (it holds the generated master key in the Compose stack).
COPY --from=build --chown=65532:65532 /out/data /data
USER nonroot:nonroot
EXPOSE 8080 8081
ENTRYPOINT ["/usr/local/bin/stampede"]
CMD ["server"]
