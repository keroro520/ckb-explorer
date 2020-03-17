#!/usr/bin/env bash

# Usage: go-build.sh PLATFORMS

set -e

# PLATFORMS=(linux/amd64 darwin/amd64 darwin/386 freebsd/386 freebsd/amd64 freebsd/arm linux/386 linux/amd64 linux/arm linux/arm64 linux/ppc64 linux/ppc64le linux/mips linux/mipsle linux/mips64 linux/mips64le)
PLATFORMS=("$@")

VERSION="${TRAVIS_TAG}"
: "${VERSION:=$(git describe --tags --always)}"
GLDFLAGS=${GLDFLAGS:-}
GLDFLAGS="$GLDFLAGS -X main.version=$VERSION"
GOFLAGS=${GOFLAGS:-}

for PLATFORM in "${PLATFORMS[@]}"; do
    PLTFRM_SPLT=(${PLATFORM//\// })
    GOOS=${PLTFRM_SPLT[0]}
    GOARCH=${PLTFRM_SPLT[1]}

    SUBDIRECTORY="ckb_exporter-${VERSION}.${GOOS}-${GOARCH}"
    FILE="${SUBDIRECTORY}/ckb_exporter"
    ARCHIVE="${SUBDIRECTORY}.tar.gz"

    mkdir -p "releases/${SUBDIRECTORY}"
    CGO_ENABLED=0 GOOS=${GOOS} GOARCH=${GOARCH} go build ${GOFLAGS} -ldflags "${GLDFLAGS}" -o "releases/${FILE}" main.go
    tar -C releases -cvzf releases/${ARCHIVE} ${FILE}
    rm -rf releases/${SUBDIRECTORY}
done
