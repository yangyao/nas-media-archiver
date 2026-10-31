# Repository layout

This is a small monorepo with a Go backend/CLI and independent native clients.
The backend remains one NAS executable; reorganizing source files does not
change its HTTP API, installation paths, persistent state or media layout.

```text
.
├── go.mod / go.sum         One Go module for the backend and CLI
├── Makefile                Build/test entry points from the repository root
├── cmd/archive/            Main Go package: CLI, HTTP service and archive engine
│   ├── archive.go          CLI commands and shared media/file helpers
│   ├── server.go           HTTP routing, authentication and SSE
│   ├── service_*.go        Scheduling, persistence, scan/transfer and review
│   ├── *_test.go           Unit, recovery, concurrency and regression tests
│   └── web/index.html      Existing embedded service status page
├── apps/macos/             Swift package, native client tests and app assets
├── packaging/qnap/         QDK inputs, lifecycle scripts and package checks
├── scripts/                Real-process HTTP smoke test
├── docs/                   User, API, architecture and validation documentation
└── dist/                   Generated executables/installers (not tracked)
```

## Build and validation

Run commands from the repository root:

```sh
make build                         # dist/archive
make build-linux-arm64             # dist/archive-linux-arm64
make build-linux-arm7              # dist/archive-linux-armv7
make test                          # go test -race ./...
make smoke                         # disposable real-process HTTP integration
make test-macos                    # Swift tests; requires macOS + Xcode 26+
make macos                         # apps/macos/dist/NASArchiver.app
make qnap-stage                    # QDK input archive, not an installer
```

Direct Go builds now use `go build ./cmd/archive`, not `go build .`. The root Go
module is retained, so `go test ./...` still finds all backend tests. The Swift
package moved from `desktop/` to `apps/macos/`; discard its old generated build
cache after moving the checkout. Installable QPKGs and architecture-specific Mac
ZIPs remain build artifacts rather than source-controlled files.

## Future Web client

Add `apps/web/` when an actual browser client is needed. It should consume the
same `/api/v1` API and SSE stream as the Mac client, without owning media moves or
adding another task database. Serve it from the Go origin to avoid a separate
NAS runtime and unnecessary cross-origin token handling.

Keep frontend sources, their package manifest/lockfile and tests in `apps/web/`.
Its release build should copy static output into a dedicated subdirectory under
`cmd/archive/web/`, then build Go to embed that output. Go embedding cannot reach
outside its package directory. The current status page is already embedded there;
there is no placeholder JavaScript framework, npm dependency or unimplemented
Web application in this version.

## Backend package boundaries

The existing CLI and service share metadata parsing and file safety helpers.
They remain one Go package for this layout-only change. If another backend entry
point or substantial Web functionality needs reusable Go APIs, extract cohesive
packages such as `internal/archiver` and `internal/server`, leaving a thin
`cmd/archive` entry point. Avoid making those packages depend on client UI code.
Do that with focused tests instead of combining it with this directory move.
