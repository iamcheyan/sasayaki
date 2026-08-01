BIN ?= sasayaki

.PHONY: build test install clean

build:
	go build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/sasayaki

test:
	go test ./...

install: build
	install -Dm755 $(BIN) $(HOME)/.local/bin/$(BIN)

clean:
	rm -rf $(BIN) dist
