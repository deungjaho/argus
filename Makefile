PREFIX  ?= $(HOME)/.local
BINDIR  ?= $(PREFIX)/bin
VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w

# 交叉编译：make build GOOS=linux GOARCH=amd64
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)

.PHONY: all build install uninstall test vet fmt fmt-check clean

all: build

build:
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags="$(LDFLAGS)" -o bin/argus .

install: build
	install -d $(BINDIR)
	install -m 0755 bin/argus $(BINDIR)/argus

uninstall:
	rm -f $(BINDIR)/argus

test:
	go test -count=1 -timeout 60s ./...

vet:
	go vet ./...

fmt:
	gofmt -w .
	goimports -w . 2>/dev/null || true

fmt-check:
	@diff=$$(gofmt -l .); if [ -n "$$diff" ]; then \
		echo "gofmt found unformatted files:"; echo "$$diff"; exit 1; fi

clean:
	rm -rf bin/
