# atomic-server image. Build context is the REPO ROOT:
#   docker build -t atomic-updater/server:dev -f server/Dockerfile .

FROM node:22-alpine AS ui
WORKDIR /build/ui
COPY server/ui/package.json server/ui/package-lock.json ./
RUN npm ci
COPY server/ui/ ./
# vite outDir is ../internal/web/dist relative to the ui root
RUN npm run build

FROM golang:1.22 AS build
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
COPY --from=ui /build/internal/web/dist ./internal/web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/atomic-server ./cmd/atomic-server

FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/atomic-server /usr/bin/atomic-server
USER 65532:65532
ENTRYPOINT ["/usr/bin/atomic-server"]
