FROM --platform=$BUILDPLATFORM golang:1.21-alpine AS builder

RUN apk add --no-cache git

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go mod tidy

# sync VERSION for go:embed
RUN cp VERSION pkg/bridge/VERSION

# copy images for go:embed (can't use symlinks or .. paths)
RUN cp assets/Icon.png pkg/ui/Icon.png && \
    cp assets/DarkLogo.png pkg/ui/DarkLogo.png

ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT

# embed version via ldflags (takes priority over go:embed)
RUN VERSION=$(cat VERSION | tr -d '[:space:]') && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOARM=${TARGETVARIANT#v} \
    go build -ldflags="-w -s -X bridge/pkg/bridge.Version=${VERSION}" -o /bridge ./cmd/bridge

FROM alpine:latest

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
