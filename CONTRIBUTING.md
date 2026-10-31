# Contributing

Thanks for contributing to `nas-media-archiver`.

## Development setup

```bash
go build -o archive ./cmd/archive
make build-linux-arm64
make build-linux-arm7
```

## Project structure

- `cmd/archive/`: Go CLI, archive engine, HTTP service and tests; one executable.
- `cmd/archive/web/`: static assets embedded in that executable.
- `apps/macos/`: independent Swift package and native app assets.
- `packaging/qnap/`: NAS lifecycle and QDK packaging.
- `scripts/`: cross-process API integration tests.
- `docs/`: usage, API, architecture and validation.
- Root `go.mod` and `Makefile`: shared Go module and build entry points.

See [repository layout](docs/repository-layout.md) before adding another client.

## Contribution guidelines

1. Keep the tool single-binary and job-oriented.
2. Keep live service state in memory and SQLite writes bounded and single-writer; preserve the CLI’s local-file persistence.
3. Preserve single-writer semantics for real archive writes unless there is a
   very strong reason to change them.
4. Avoid hard-coding machine-specific paths, hosts, or credentials.
5. Update docs when behavior changes.

## Before opening a PR

Please make sure:

- `make build build-linux-arm64 build-linux-arm7` passes
- `make test smoke test-macos` passes (macOS/Xcode required for the Swift tests)
- docs match the implemented behavior
- no private environment details are introduced
- examples use generic placeholder paths

## Scope

Good contribution areas:

- metadata extraction improvements
- duplicate-resolution correctness
- performance improvements with measured impact
- documentation clarity
- better handling of unsupported formats

