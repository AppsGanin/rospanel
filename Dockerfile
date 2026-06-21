# syntax=docker/dockerfile:1

# 1) Build the React SPA. Arch-independent → build on the native build platform
#    (avoids emulating Node when targeting arm64).
FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /web
COPY web/package*.json ./
RUN npm install
COPY web/ ./
RUN npm run build

# 2) Build the Go binary (embeds the SPA from /web/dist). CGO is off, so we
#    cross-compile from the build platform to $TARGETARCH instead of emulating.
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w" -o /out/rospanel ./cmd/rospanel

# 3) Fetch the official Xray-core binary for the target architecture.
FROM --platform=$BUILDPLATFORM debian:stable-slim AS xray
ARG XRAY_VERSION=v26.6.1
ARG TARGETARCH
# SHA256 of each release zip for XRAY_VERSION (from XTLS's published .dgst files).
# The download is rejected on mismatch, before it is unpacked and run as root.
# Update these together with XRAY_VERSION (and keep them in sync with
# internal/xray/install.go's pinnedSHA256).
RUN apt-get update && apt-get install -y --no-install-recommends curl unzip ca-certificates \
 && case "$TARGETARCH" in \
      amd64) XF=Xray-linux-64.zip; SHA=136e822e99e616692550723e8b607cd8858c62a390aea5704938bc27930904ba ;; \
      arm64) XF=Xray-linux-arm64-v8a.zip; SHA=5c0f397b2605a853e5b308ae6485ad2ec395ca11ed7ae0fd023c480c7fdb279d ;; \
      *) echo "unsupported TARGETARCH: $TARGETARCH" >&2; exit 1 ;; \
    esac \
 && curl -sL -o /tmp/x.zip "https://github.com/XTLS/Xray-core/releases/download/${XRAY_VERSION}/${XF}" \
 && echo "${SHA}  /tmp/x.zip" | sha256sum -c - \
 && unzip -o /tmp/x.zip -d /usr/local/bin xray \
 && chmod +x /usr/local/bin/xray

# 4) Runtime image (built for the target platform).
FROM debian:stable-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      nftables iptables ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/rospanel /usr/local/bin/rospanel
COPY --from=xray /usr/local/bin/xray /usr/local/bin/xray
ENV XRAY_BIN=/usr/local/bin/xray \
    ROSPANEL_DATA=/data \
    ROSPANEL_ADMIN_ADDR=127.0.0.1:8080
VOLUME /data
ENTRYPOINT ["/usr/local/bin/rospanel"]
