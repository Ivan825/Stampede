# syntax=docker/dockerfile:1

# ---- build ---------------------------------------------------------------
FROM golang:1.27-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/shoplab ./cmd/shoplab

# ---- runtime -------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/shoplab /shoplab
USER nonroot:nonroot
EXPOSE 8090
HEALTHCHECK --interval=10s --timeout=4s --start-period=60s --retries=3 \
  CMD ["/shoplab", "healthcheck"]
ENTRYPOINT ["/shoplab"]
CMD ["serve"]
