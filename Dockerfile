FROM --platform=$BUILDPLATFORM golang:1.21-alpine AS builder

RUN apk add --no-cache git

WORKDIR /build

COPY go.mod go.sum ./

RUN go mod download

COPY . .

# ensure dependencies are resolved
RUN go mod tidy

# build args for cross compilation
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT

# build the binary (Docker mode, no GUI - default build excludes gui tag)
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOARM=${TARGETVARIANT#v} \
    go build -ldflags="-w -s" -o /bridge ./cmd/bridge

FROM alpine:latest

# install ca-certificates for MQTT TLS connections
RUN apk add --no-cache ca-certificates tzdata

RUN addgroup -g 1000 bridge && \
    adduser -D -u 1000 -G bridge bridge

COPY --from=builder /bridge /usr/local/bin/bridge

USER bridge

WORKDIR /home/bridge

ENV BRIDGE_ID="" \
    SERIAL_PORT="" \
    BRIDGE_GUI="false"

ENTRYPOINT ["/usr/local/bin/bridge"]