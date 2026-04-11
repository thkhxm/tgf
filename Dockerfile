# tgf 主模块的多阶段构建示例。
#
# 本 Dockerfile 只演示 tgf/ 作为库的可构建性（build stage 编译 ./...），
# 真正业务镜像应由业务 repo 自己写 Dockerfile 并 go get/replace 引用 tgf。
#
# 构建：
#   docker build -t tgf:dev .
# 如需走国内代理：
#   docker build --build-arg GOPROXY=https://goproxy.cn,direct -t tgf:dev .

ARG GO_VERSION=1.24.7
FROM golang:${GO_VERSION}-alpine AS build

ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ENV CGO_ENABLED=0 \
    GOPROXY=${GOPROXY} \
    GOSUMDB=${GOSUMDB}

WORKDIR /src

# 先单独 copy go.mod/go.sum 利用 layer cache
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# 只做编译检查，产物丢弃；真正可执行文件由业务 repo 构建
RUN go build ./...

FROM alpine:3.20 AS runtime
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
# 业务镜像应 COPY --from=build /src/bin/<service> /app/<service>
CMD ["/bin/sh", "-c", "echo 'tgf is a library; build your own service image'"]
