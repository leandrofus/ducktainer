# Build environment for the rpm/deb packages (Debian 12). The server binary only
# needs glibc ≥ 2.34 (checked in build.sh): Debian 12+, Ubuntu 22.04+, RHEL 9+, Fedora.
# The agent binaries nodes download are static, so nodes have no glibc requirement.
FROM debian:bookworm
ARG GO_VERSION=1.26.3
ARG NFPM_VERSION=v2.43.0
RUN dpkg --add-architecture arm64 \
 && apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl git gcc libc6-dev libpam0g-dev \
      gcc-aarch64-linux-gnu libc6-dev-arm64-cross libpam0g-dev:arm64 \
 && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz | tar -C /usr/local -xz
ENV PATH=/usr/local/go/bin:$PATH GOTOOLCHAIN=local
RUN GOBIN=/usr/local/bin go install github.com/goreleaser/nfpm/v2/cmd/nfpm@${NFPM_VERSION}
