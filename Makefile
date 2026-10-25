.PHONY: test build dist clean

test:
	go test -race -cover .
	go vet .

build:
	mkdir -p dist/linux-amd64 dist/linux-arm64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o dist/linux-amd64/claude-session-sync .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='-s -w' -o dist/linux-arm64/claude-session-sync .

dist: build
	cp README.md README_VI.md LICENSE NOTICE dist/linux-amd64/
	cp README.md README_VI.md LICENSE NOTICE dist/linux-arm64/
	mkdir -p dist/linux-amd64/docs dist/linux-arm64/docs
	cp docs/VERIFICATION.md dist/linux-amd64/docs/
	cp docs/VERIFICATION.md dist/linux-arm64/docs/
	tar -czf dist/claude-desktop-session-sync-linux-0.2.0-linux-amd64.tar.gz -C dist/linux-amd64 .
	tar -czf dist/claude-desktop-session-sync-linux-0.2.0-linux-arm64.tar.gz -C dist/linux-arm64 .

clean:
	rm -rf dist
