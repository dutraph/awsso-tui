BIN      ?= awsso
PREFIX   ?= /usr/local
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)

.PHONY: all build install uninstall tidy run clean test

all: build

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BIN) .

install: build
	@mkdir -p $(PREFIX)/bin
	install -m 0755 bin/$(BIN) $(PREFIX)/bin/$(BIN)
	@echo "installed $(PREFIX)/bin/$(BIN)"

uninstall:
	rm -f $(PREFIX)/bin/$(BIN)

tidy:
	go mod tidy

run: build
	./bin/$(BIN)

clean:
	rm -rf bin

test:
	go test ./...
