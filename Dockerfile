# Build assets
FROM --platform=$BUILDPLATFORM node:25.9.0-alpine AS node

RUN npm install -g --force corepack && corepack enable

ENV CI=true

WORKDIR /build

# Install dependencies from lock file
COPY pnpm-lock.yaml pnpm-workspace.yaml ./
RUN pnpm fetch --ignore-scripts

# Copy package.json and install dependencies
COPY package.json ./
RUN pnpm install --offline --ignore-scripts

# Copy assets and translations to build
COPY vite.config.ts tsconfig.json .prettierrc.cjs .npmrc ./
COPY assets ./assets
COPY locales ./locales
COPY public ./public

ARG CLOUD_URL
ENV CLOUD_URL=$CLOUD_URL

# Build assets
RUN pnpm build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

RUN apk add --no-cache ca-certificates openssl && mkdir /dozzle

WORKDIR /dozzle

# Copy go mod files
COPY go.* ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# Copy all other files
COPY internal ./internal
COPY proto ./proto
COPY types ./types
COPY main.go ./
COPY protos ./protos
RUN openssl genpkey -algorithm Ed25519 -out shared_key.pem && \
    openssl req -new -key shared_key.pem -out shared_request.csr -subj "/C=US/ST=California/L=San Francisco/O=Dozzle" && \
    openssl x509 -req -in shared_request.csr -signkey shared_key.pem -out shared_cert.pem -days 1825 && \
    rm shared_request.csr

# Copy assets built with node
COPY --from=node /build/dist ./dist

# Args
ARG TAG=dev
ARG TARGETOS TARGETARCH

# Build binary
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
  GOOS=$TARGETOS GOARCH=$TARGETARCH CGO_ENABLED=0 go build -ldflags "-s -w -X github.com/amir20/dozzle/internal/support/cli.Version=$TAG" -o dozzle

RUN mkdir /data

FROM scratch

COPY --from=builder /data /data
COPY --from=builder /tmp /tmp
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /dozzle/dozzle /dozzle

EXPOSE 8080

ENTRYPOINT ["/dozzle"]
