# 樱隧 Sakura Tunnel · 多阶段构建
#   docker build -f docker/Dockerfile -t sakura-tunnel/server:latest .
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false -tags noweb \
    -ldflags "-s -w" -o /out/sakura ./cmd/sakura

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && adduser -D -H -u 10001 sakura
COPY --from=build /out/sakura /usr/local/bin/sakura
USER sakura
WORKDIR /data
VOLUME ["/data"]
EXPOSE 7443 7500
ENTRYPOINT ["sakura"]
CMD ["-c", "/data/server.toml"]
