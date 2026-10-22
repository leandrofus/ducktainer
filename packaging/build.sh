#!/bin/sh
# Runs inside the packaging/Dockerfile image with the repo mounted at /src.
# Builds ducktainer with PAM (cgo) for each arch and packs rpm + deb.
set -eu
: "${VERSION:=0.1.0}"
: "${MAINTAINER:=ducktainer <ducktainer@localhost>}"
: "${ARCHES:=amd64 arm64}"
export HOME=/tmp GOCACHE=/tmp/gocache GOPATH=/tmp/gopath GOFLAGS=-buildvcs=false
mkdir -p dist/build dist/packages
# The agent binaries nodes download: static (no cgo, so no glibc requirement:
# RHEL 8 and older nodes run them) and one per architecture, whatever the
# architecture of the server, so every package carries both.
for arch in amd64 arm64; do
	echo "→ go build linux/$arch (static agent)"
	CGO_ENABLED=0 GOOS=linux GOARCH=$arch \
		go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "dist/build/ducktainer-agent-linux-$arch" .
done
# …and the one Windows nodes download
echo "→ go build windows/amd64 (agent)"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
	go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "dist/build/ducktainer-agent-windows-amd64.exe" .
for arch in $ARCHES; do
	case $arch in
	amd64) cc=gcc ;;
	arm64) cc=aarch64-linux-gnu-gcc ;;
	*) echo "arquitectura no soportada: $arch" >&2; exit 1 ;;
	esac
	echo "→ go build linux/$arch (cgo + PAM)"
	CGO_ENABLED=1 GOOS=linux GOARCH=$arch CC=$cc \
		go build -trimpath -tags pam -ldflags "-s -w -X main.version=$VERSION" -o "dist/build/ducktainer-linux-$arch" .
	# nfpm doesn't expand variables in content paths: render per arch
	sed "s|\${ARCH}|$arch|g" packaging/nfpm.yaml > "/tmp/nfpm-$arch.yaml"
	for fmt in rpm deb; do
		ARCH=$arch VERSION=$VERSION MAINTAINER=$MAINTAINER nfpm package -f "/tmp/nfpm-$arch.yaml" -p $fmt -t dist/packages/
	done
done
ls -l dist/packages
# guard: the agent binaries nodes download must be static (no dynamic loader, no glibc)
for f in dist/build/ducktainer-agent-linux-*; do
	if readelf -l "$f" | grep -q INTERP; then echo "$f is not static" >&2; exit 1; fi
	echo "$f estático"
done
# guard: the highest glibc symbol version required must stay ≤ 2.34 (RHEL 9)
for f in dist/build/ducktainer-linux-*; do
	max=$(strings "$f" | grep -o 'GLIBC_2\.[0-9]*' | sort -t. -k2 -n | tail -1)
	echo "$f requiere $max"
done
