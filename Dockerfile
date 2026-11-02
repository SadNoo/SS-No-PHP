# 构建：docker build -t niuniupanel .
# 程序只有一个可执行文件，配置全部可以用环境变量提供（NIUNIU_<段名>_<键名>），也可以挂载配置文件后用 -config 指定。
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/panel ./cmd/panel

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && adduser -D -H -h /var/lib/niuniupanel panel \
    && install -d -o panel -g panel /var/lib/niuniupanel /var/lib/niuniupanel/data
COPY --from=build /out/panel /usr/local/bin/panel
USER panel
WORKDIR /var/lib/niuniupanel
ENV NIUNIU_HTTP_LISTEN=0.0.0.0:8080
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=20s CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["panel"]
CMD ["serve"]
