GO ?= go
PREFIX ?= $(HOME)/.local
VERSION ?= dev
LDFLAGS = -s -w -X github.com/mengdeerer/gdesk/internal/app.Version=$(VERSION)

.PHONY: build test demo install
build:
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/gdesk ./cmd/gdesk

test:
	$(GO) test ./...
	python3 -m unittest discover -s tests -v

demo: build
	./bin/gdesk --demo

install: build
	install -d "$(PREFIX)/bin"
	install -m 755 bin/gdesk "$(PREFIX)/bin/gdesk"
	@echo "已安装到 $(PREFIX)/bin/gdesk（需在 PATH 中）"
