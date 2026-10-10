# Contributing

Bug reports and ideas are welcome in
[issues](https://github.com/ipoluianov/althex/issues). Please include the
AltHex version (shown in About) and your operating system.

## Building

You need Go, see the version in [go.mod](go.mod). No cgo is required, so
any platform builds for any other.

```sh
go build .
go test ./...
```

The UI library is [nui](https://github.com/ipoluianov/nui). To work on both
at once, uncomment the `replace` line at the end of `go.mod`.

## Code layout

- `core/` - the model, with no GUI: `core/source` reads files through a
  page cache, `core/doc` is the document (pieces, history, journal, saving);
  `codec` (encodings, Intel HEX, S-record, arrays), `hash` (checksums,
  entropy), `search`, `interp` (the data interpreter), `expr` (offset
  expressions), `diff`, `patch`, `ops` (fill, bitwise operations), `tmpl`
  (the language of templates; `core/tmpl/library` - the built-in templates,
  described in [docs/templates.md](docs/templates.md)); `image` (disk
  image containers), `part` (partition tables), `fs` (file systems),
  `bootimg` (boot images), `disasm` (disassemblers), `fw` (firmware
  formats and architecture detection), `periph` (the registers of
  microcontrollers, generated from SVD/ATDF by `core/periph/gen`; see
  `core/periph/data/SOURCES.md` for the licences), `analysis` (recursive
  disassembly, functions, references, the listing and its .s export),
  `api` (what the external interfaces can do: the tools, over a Workspace
  - the window or the files of a command)
- `rpc/` - JSON-RPC 2.0 and MCP, their transports (stdio, unix socket,
  named pipe, Streamable HTTP); generic, see [docs/api.md](docs/api.md)
- `cli/` - the commands without a window: `althex help`
- `forms/` - the windows, dialogs and widgets; `forms/strings.go` holds
  all the texts, `forms/hexview` is the hex view
- `config/` - settings and the window layout
- `install/` - installing a downloaded copy on Windows
- `instance/` - keeps a single running copy, a second start passes its files to it
- `build/` - release builds and packages, `scripts/` - the Linux installer,
  `fuzz.sh`

Tests that need GNU binutils (`arm-none-eabi-*`, `avr-*`,
`riscv64-unknown-elf-*`, `xtensa-lx106-elf-*`) find them in the directory
of `ALTHEX_BINUTILS` or on the PATH and are skipped without them. File
system tests use `mkfs.*` from `/sbin`: run `PATH=$PATH:/sbin go test ./...`.
`ALTHEX_FW_SAMPLES` names a directory of real firmwares (not in the
repository): `core/analysis` analyzes each, exports it and, with binutils,
checks that it assembles back to the same bytes; `core/fw` checks the ESP
images there.

The parsers of files, templates and messages have fuzz targets (`Fuzz*`).
`scripts/fuzz.sh [duration] [jobs] [regexp]` runs each of them in turn
(`scripts/fuzz.sh 60s 2` for a quick pass, `scripts/fuzz.sh 6h 4` for a long
one); a failing input is saved under `testdata/fuzz/` of its package and
becomes a test case: commit it with the fix. `internal/fuzzguard` turns an
input that runs too long into a crash, so a hang is caught too.

A release: `build/all.sh` builds every platform with `SHA256SUMS`;
`scripts/smoke.sh bin/<dir>` checks it; `scripts/startup-time.sh BINARY`
measures the cold start (PERF-1). After changing the dependencies, run
`go run ./build/notices`: it rewrites `app/notices.txt`, the licences shown
by About and `althex licenses` and shipped in the packages.

See [PLAN.md](PLAN.md) for the parts still to come.

## Pull requests

- Keep a change to one thing, and describe what it does and why.
- Every text goes into all the languages in `forms/strings.go`.
  `TestAllTranslated` fails on a missing one. A machine translation is
  fine, a native speaker can fix it later.
- Say on which systems you tried it.
