# Do not pin the builder to BUILDPLATFORM: the fallback GOOS/GOARCH below also
# works with the legacy Docker builder, which does not provide those ARGs.
FROM golang:1.27.4-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS build

ARG TARGETOS
ARG TARGETARCH
# Empty by default: the build then embeds the repository VERSION file.
ARG VERSION=
ARG COMMIT=unknown
ARG BUILD_TIME=unknown
ARG GOPROXY=https://proxy.golang.org,direct

ENV CGO_ENABLED=0 \
    GOPROXY=${GOPROXY}

# Go module downloads need CA roots in the build stage.
RUN apk add --no-cache ca-certificates

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN set -eux; \
    target_os="${TARGETOS:-$(go env GOOS)}"; \
    target_arch="${TARGETARCH:-$(go env GOARCH)}"; \
    version="${VERSION:-$(tr -d '\r' < VERSION)}"; \
    case "${version}" in v*) ;; *) version="v${version}" ;; esac; \
    mkdir -p /out/rootfs-app/data /out/rootfs-root/tmp /out/rootfs-root/var/run; \
    chmod 0700 /out/rootfs-app/data; \
    chmod 1777 /out/rootfs-root/tmp; \
    GOOS="${target_os}" GOARCH="${target_arch}" go build \
        -trimpath \
        -buildvcs=false \
        -ldflags="-s -w \
            -X github.com/fynhq/windwatch/internal/version.Version=${version} \
            -X github.com/fynhq/windwatch/internal/version.Commit=${COMMIT} \
            -X github.com/fynhq/windwatch/internal/version.BuildTime=${BUILD_TIME}" \
        -o /out/windwatch ./cmd/windwatch

FROM scratch

COPY --from=build --chown=65532:65532 /out/rootfs-app/ /
COPY --from=build /out/rootfs-root/ /
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/windwatch /windwatch
COPY THIRD_PARTY_NOTICES.md /THIRD_PARTY_NOTICES.md

LABEL org.opencontainers.image.source="https://github.com/fynhq/windwatch" \
      org.opencontainers.image.licenses="MIT"

ENV WINDWATCH_ADDR=:8080 \
    WINDWATCH_DATA_DIR=/data

USER 65532:65532
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/windwatch", "healthcheck"]

ENTRYPOINT ["/windwatch"]
CMD ["serve"]
