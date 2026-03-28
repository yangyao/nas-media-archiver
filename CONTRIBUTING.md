# Contributing

Thanks for contributing to `nas-media-archiver`.

## Development setup

```bash
go build -o archive .
make build-linux-arm64
make build-linux-arm7
```

## Project structure

- `archive.go`: main CLI implementation
- `docs/`: user and design documentation
- `Makefile`: common build targets

## Contribution guidelines

1. Keep the tool single-binary and job-oriented.
2. Prefer simple local-file persistence over introducing a service or database.
3. Preserve single-writer semantics for real archive writes unless there is a
   very strong reason to change them.
4. Avoid hard-coding machine-specific paths, hosts, or credentials.
5. Update docs when behavior changes.

## Before opening a PR

Please make sure:

- the project builds locally
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

