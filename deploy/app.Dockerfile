# Build context: repository root. Targets: app (the autograder binary, one image for all its processes), tools (poppler
# utilities and toolsd, isolated).
FROM node:lts AS web
WORKDIR /web
COPY src/web/package.json src/web/package-lock.json ./
RUN npm ci --no-audit --no-fund --loglevel=error
COPY src/web/ ./
RUN npm run build

FROM golang:1.27 AS build
WORKDIR /src
COPY src/backend/go.mod src/backend/go.sum ./
RUN go mod download
COPY src/backend/ ./
COPY --from=web /web/dist/ ./internal/delivery/httpapi/web/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/autograder ./cmd/autograder \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/toolsd ./cmd/toolsd

# Minimal runtime image: static binary, no shell, no package manager.
FROM gcr.io/distroless/static-debian13:nonroot AS app
COPY --from=build /out/autograder /autograder
ENTRYPOINT ["/autograder"]
CMD ["web"]

# Document utilities: poppler only. Runs without network, as nobody, read-only (compose).
FROM debian:stable-slim AS tools
RUN apt-get update && apt-get install -y --no-install-recommends poppler-utils && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/toolsd /toolsd
USER 65534:65534
ENTRYPOINT ["/toolsd"]
