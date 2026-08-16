BIN ?= sasayaki

.PHONY: build test install clean dist/rpm dist/deb

build:
	go build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/sasayaki

test:
	go test ./...

install: build
	ln -sfn $(CURDIR)/$(BIN) $(HOME)/.local/bin/$(BIN)

# Distribution packaging. See packaging/{rpm,deb}/README.md.
dist/rpm: scripts/build-rpm.sh packaging/rpm/sasayaki.spec
	./scripts/build-rpm.sh

dist/deb: scripts/build-deb.sh packaging/deb/control
	./scripts/build-deb.sh

clean:
	rm -rf $(BIN) dist
