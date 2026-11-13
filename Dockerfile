# syntax=docker/dockerfile:1

FROM golang:1.27.4 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
# Cache mounts keep module and build caches between builds; they never end up in the image.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/keygen ./cmd/keygen

# distroless: no shell, no package manager; runs as uid 65532 (nonroot).
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/api /out/keygen /usr/local/bin/
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=3 CMD ["api", "healthcheck"]
CMD ["api"]
