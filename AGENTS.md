# AGENTS.md

Conventions for agents and contributors working in this repository.

## Build and test

```sh
make build    # tests, then builds bin/ak820
make test     # go test ./...
make vet      # go vet ./...
make fmt      # gofmt
```

All three (test, vet, gofmt-clean) must pass before committing. Go 1.22+,
stdlib only — do not add dependencies.

## Layout

- `cmd/ak820/` — CLI entry point and command wiring
- `internal/protocol/` — wire format, command table, palette, key map.
  This is the single source of truth for the protocol; never duplicate
  frame-building logic elsewhere.
- `internal/device/` — hidraw discovery, open/close, framed send/receive
- `internal/game/`, `internal/viz/`, `internal/termui/` — interactive features

## Hardware safety rules

- Write only to the vendor interface (usage page `0xFF02`, 64-byte reports,
  no report IDs). Never open interface 1 or 2 for writing.
- `internal/protocol` documents each command's verification status. Commands
  marked send-only or refuted must not gain user-facing behavior without new
  hardware verification.
- Destructive commands (`reset`, `raw`) require `--yes` and must not be made
  easier to invoke.
- Interactive commands must restore keyboard lighting and terminal state on
  every exit path, including signals.

## Documentation style

Follow the [Google developer documentation style guide](https://developers.google.com/style)
and keep documentation timeless: no dates, no session narratives, no
"recently" or "as of now". Describe the system as it is.

## CLI conventions

Agents are a primary audience. Follow the manifest in `ak820 agent-context`:
stable verbs (get/list/set), `--json` on data output, stdout for data,
stderr for logs, enumerated valid values in errors, `--dry-run` for
mutations, stable exit codes (0 ok, 1 usage, 2 device error, 3 not found).
