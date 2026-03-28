APP=archive
CLI_PKG=.
DIST=dist

.PHONY: build build-linux-arm64 build-linux-arm7 clean

build:
	go build -o $(DIST)/$(APP) $(CLI_PKG)

build-linux-arm64:
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o $(DIST)/$(APP)-linux-arm64 $(CLI_PKG)

build-linux-arm7:
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -o $(DIST)/$(APP)-linux-armv7 $(CLI_PKG)

clean:
	rm -rf $(DIST)
