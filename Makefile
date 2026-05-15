PREFIX ?= $(HOME)/.local
BIN := $(PREFIX)/bin/gutter

.PHONY: build install clean run

build:
	go build -o gutter .

install:
	go build -o $(BIN) .
	@echo "installed $(BIN)"

clean:
	rm -f gutter

run: build
	./gutter
