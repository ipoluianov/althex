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
  described in [docs/templates.md](docs/templates.md))
- `cli/` - the commands without a window: `althex help`
- `forms/` - the windows, dialogs and widgets; `forms/strings.go` holds
  all the texts, `forms/hexview` is the hex view
- `config/` - settings and the window layout
- `install/` - installing a downloaded copy on Windows
- `instance/` - keeps a single running copy, a second start passes its files to it
- `build/` - release builds and packages, `scripts/` - the Linux installer

See [PLAN.md](PLAN.md) for the parts still to come.

## Pull requests

- Keep a change to one thing, and describe what it does and why.
- Every text goes into all the languages in `forms/strings.go`.
  `TestAllTranslated` fails on a missing one. A machine translation is
  fine, a native speaker can fix it later.
- Say on which systems you tried it.
