# {{PROJECT_NAME}} 业务镜像（多阶段构建，go 1.26）
#
# 单 main 项目（single_process / http_rest / http_rpc 模板）：
#   docker build -t {{PROJECT_NAME}}:dev .
# 多 main 项目（distributed 模板）用 MAIN_PKG 选进程：
#   docker build --build-arg MAIN_PKG=./cmd/gateway -t {{PROJECT_NAME}}-gateway:dev .
#   docker build --build-arg MAIN_PKG=./cmd/game    -t {{PROJECT_NAME}}-game:dev .
# 国内网络加：
#   --build-arg GOPROXY=https://goproxy.cn,direct

ARG GO_VERSION=1.26
FROM golang:${GO_VERSION}-alpine AS build

ARG GOPROXY=https://proxy.golang.org,direct
ENV CGO_ENABLED=0 \
    GOPROXY=${GOPROXY}

WORKDIR /src

# 先 copy go.mod/go.sum 利用 layer cache
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG MAIN_PKG=.
RUN go build -trimpath -ldflags "-s -w" -o /out/app ${MAIN_PKG}

FROM alpine:3.21 AS runtime
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/app /app/app
# .env.<TGFMODULE> 随镜像分发（敏感项建议运行期 env 覆盖，而非烧进镜像）
COPY .env.* /app/

# TGFMODULE 选环境（dev/test/release）；框架捕获 SIGTERM 优雅停机，无需额外脚本
ENV TGFMODULE=release
ENTRYPOINT ["/app/app"]
