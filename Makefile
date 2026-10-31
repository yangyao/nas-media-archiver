APP=archive
CLI_PKG=./cmd/archive
DIST=dist

.PHONY: build build-linux-arm64 build-linux-arm7 test smoke macos qnap-stage qnap-package test-macos clean

build:
	mkdir -p $(DIST)
	go build -o $(DIST)/$(APP) $(CLI_PKG)

build-linux-arm64:
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o $(DIST)/$(APP)-linux-arm64 $(CLI_PKG)

build-linux-arm7:
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -o $(DIST)/$(APP)-linux-armv7 $(CLI_PKG)

test:
	go test -race ./...

smoke: build
	python3 scripts/smoke-service.py --binary $(DIST)/$(APP)

test-macos:
	swift test --package-path apps/macos

macos:
	cd apps/macos && ./scripts/package-macos.sh

qnap-stage: build-linux-arm64
	packaging/qnap/build.sh --stage-only

qnap-package: build-linux-arm64
	packaging/qnap/build.sh

clean:
	rm -rf $(DIST)
