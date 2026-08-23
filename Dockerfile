ARG BUILD_VERSION=dev
ARG VCS_REF=unknown

FROM oven/bun:1@sha256:0733e50325078969732ebe3b15ce4c4be5082f18c4ac1a0f0ca4839c2e4e42a7 AS builder

ARG BUILD_VERSION

WORKDIR /build
COPY web/default/package.json .
COPY web/default/bun.lock .
RUN bun install --frozen-lockfile
COPY ./web/default .
RUN DISABLE_ESLINT_PLUGIN='true' VITE_REACT_APP_VERSION="${BUILD_VERSION}" bun run build

FROM alpine:3 AS builder-classic
# Classic frontend disabled (not used in this deployment).
RUN mkdir -p /build/dist && echo '<!doctype html><html><body>classic disabled</body></html>' > /build/dist/index.html

FROM golang:1.26.1-alpine@sha256:2389ebfa5b7f43eeafbd6be0c3700cc46690ef842ad962f6c5bd6be49ed82039 AS builder2
ARG BUILD_VERSION
ENV GO111MODULE=on CGO_ENABLED=0

ARG TARGETOS
ARG TARGETARCH
ENV GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64}
ENV GOEXPERIMENT=greenteagc

WORKDIR /build

ADD go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=builder /build/dist ./web/default/dist
COPY --from=builder-classic /build/dist ./web/classic/dist
RUN go build -ldflags "-s -w -X 'github.com/QuantumNous/new-api/common.Version=${BUILD_VERSION}'" -o new-api

FROM debian:bookworm-slim@sha256:f06537653ac770703bc45b4b113475bd402f451e85223f0f2837acbf89ab020a

ARG BUILD_VERSION
ARG VCS_REF
LABEL org.opencontainers.image.source="https://github.com/lisaluoyf/new-api" \
      org.opencontainers.image.version="${BUILD_VERSION}" \
      org.opencontainers.image.revision="${VCS_REF}"

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata libasan8 wget \
    && rm -rf /var/lib/apt/lists/* \
    && update-ca-certificates

COPY --from=builder2 /build/new-api /
COPY LICENSE NOTICE THIRD-PARTY-LICENSES.md /licenses/
EXPOSE 3000
WORKDIR /data
ENTRYPOINT ["/new-api"]
