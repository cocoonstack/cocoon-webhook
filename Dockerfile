FROM --platform=$BUILDPLATFORM golang:1.26.5-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG REVISION=unknown
ARG BUILTAT=unknown
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath \
      -ldflags="-s -w \
        -X github.com/cocoonstack/cocoon-webhook/version.VERSION=${VERSION} \
        -X github.com/cocoonstack/cocoon-webhook/version.REVISION=${REVISION} \
        -X github.com/cocoonstack/cocoon-webhook/version.BUILTAT=${BUILTAT}" \
      -o /out/cocoon-webhook .

FROM alpine:3.21 AS runtime-deps
RUN apk add --no-cache ca-certificates

FROM busybox:stable-musl
COPY --from=runtime-deps /etc/ssl/certs/ /etc/ssl/certs/
COPY --from=build /out/cocoon-webhook /usr/bin/cocoon-webhook

EXPOSE 8443
ENTRYPOINT ["/usr/bin/cocoon-webhook"]
