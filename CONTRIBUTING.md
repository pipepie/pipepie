# Contributing to Pipepie

Thanks for your interest in contributing!

## Quick Start

```bash
git clone https://github.com/pipepie/pipepie
cd pipepie
make build    # build the binary
make test     # run tests
make dev      # build + run server locally
```

## Project Structure

```
cmd/            CLI commands (cobra)
internal/
  client/       Tunnel client (Noise NK, forwarder, display)
  server/       Relay server (HTTP mux, hub, tunnel listener, API, dashboard)
  protocol/     Wire protocol (Noise, Protobuf, zstd)
  store/        SQLite storage
  config/       YAML + client config
  setup/        Interactive setup wizard
  doctor/       Server diagnostics
  ui/           Embedded web dashboard
proto/          Protobuf schema
demo/           VHS tape files for GIFs
```

## Making Changes

1. Fork the repo
2. Create a branch: `git checkout -b my-feature`
3. Make your changes
4. Run tests: `make test`
5. Commit with a clear message
6. Open a PR

## What to Work On

- Issues labeled [`good first issue`](https://github.com/pipepie/pipepie/labels/good%20first%20issue) are great starting points
- Check [Discussions](https://github.com/pipepie/pipepie/discussions) for feature ideas
- Bug fixes are always welcome

## Code Style

- Go standard formatting (`gofmt`)
- Structured logging via `log/slog`
- Errors returned, not panicked
- Tests for new features

## Protobuf

If you change `proto/wire.proto`:

```bash
make proto
```

Requires `protoc` and `protoc-gen-go`.

## Need Help?

Open a [Discussion](https://github.com/pipepie/pipepie/discussions) — we're happy to help.
