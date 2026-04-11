# tgf 主模块构建 / 测试 / 静态检查入口
#
# 本 Makefile 在 tgf/ 子目录下运行，假设已经配置了 Go 1.24+ 工具链。
# 中国大陆环境下建议预先导出：
#   export GOSUMDB=sum.golang.google.cn
#   export GOPROXY=https://goproxy.cn,direct
# Windows 开发机需要额外导出 GOOS=windows（否则 go test 默认生成 ELF 无法直接跑）。
#
# 常用目标：
#   make build        编译主模块
#   make test         run unit tests (-race -count=1)
#   make race         等价 test (保留兼容)
#   make vet          go vet
#   make lint         golangci-lint run（需要先装 golangci-lint）
#   make cover        生成 coverage.out 并打印行覆盖率
#   make bench        跑所有 Benchmark
#   make tidy         go mod tidy
#   make fmt          gofmt -w .
#   make all          vet + lint + test
#   make clean        清理产物

GO        ?= go
GOFLAGS   ?=
PKG       ?= ./...
RACE      ?= -race
TIMEOUT   ?= 5m
COVERFILE ?= coverage.out
LINT      ?= golangci-lint

.PHONY: all build test race vet lint cover bench tidy fmt clean help

all: vet lint test

build:
	$(GO) build $(GOFLAGS) $(PKG)

test:
	$(GO) test $(GOFLAGS) $(RACE) -count=1 -timeout=$(TIMEOUT) $(PKG)

race: test

vet:
	$(GO) vet $(PKG)

lint:
	@command -v $(LINT) >/dev/null 2>&1 || { \
		echo "golangci-lint 未安装，参考 https://golangci-lint.run/usage/install/"; \
		exit 1; \
	}
	$(LINT) run ./...

cover:
	$(GO) test $(GOFLAGS) $(RACE) -count=1 -timeout=$(TIMEOUT) -coverprofile=$(COVERFILE) $(PKG)
	$(GO) tool cover -func=$(COVERFILE) | tail -1

bench:
	$(GO) test $(GOFLAGS) -run=^$$ -bench=. -benchmem $(PKG)

tidy:
	$(GO) mod tidy

fmt:
	gofmt -w .

clean:
	rm -f $(COVERFILE)

help:
	@echo "make [build|test|vet|lint|cover|bench|tidy|fmt|clean|all]"
