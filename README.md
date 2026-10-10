# AltHex

A hex editor for files of any size, disks, disk images and firmware. It
runs on Linux, Windows and macOS as a single binary.

- **Files of any size.** A 100 GB file opens at once. Edits, inserts and
  deletes take milliseconds, and undo has no limit. Unsaved edits survive a
  crash.
- **Structures.** Templates parse PNG, ELF, PE, ZIP, MBR, GPT, FAT, NTFS,
  ext4, ISO, UDF, VHD/VMDK/QCOW2, Android boot images, UF2 and many other
  formats. The template language is close to 010 Editor's, and you can
  write your own.
- **Disks and images.** Disks open read-only and show their partition
  tables and file trees. You can extract files from them. Problems in the
  partition tables are highlighted.
- **Firmware.**
  - Formats: Intel HEX, S-record, ELF, UF2, DfuSe and ESP images.
  - Disassemblers: ARM Cortex-M, AVR, RISC-V, Xtensa, ARM64 and x86.
  - Analysis: functions, cross-references, a call graph, and peripheral
    registers named for 154 microcontrollers.
  - Export to assembler source that GNU as builds back into the same bytes.
- **Search and compare.** Search bytes with wildcards, text in 23
  encodings, or typed numbers, at gigabytes per second. Compare files with
  or without shifts, and make IPS, BPS or hex-diff patches.
- **Automation.**
  - A command line for hashes, search and replace, patches, conversion and
    parsing.
  - A local JSON-RPC API.
  - An MCP server for AI agents. It is off by default, takes local
    connections only, and asks the user before any change.
- **Languages.** The interface is in 12 languages.

Website and documentation: https://altbins.pro/althex/. The docs are also
in this repository:

- [User guide](docs/user-guide.md)
- [Template language](docs/templates.md)
- [External interfaces (MCP, API)](docs/api.md)

## Building

You need Go (see the version in [go.mod](go.mod)). No cgo is required.

```sh
go build .
```

`build/all.sh` (or `build/all.bat`) builds every platform into `bin/`: the
binaries, the deb/rpm packages, the dmg, the installer script and
`SHA256SUMS`. `scripts/smoke.sh bin/<dir>` checks a build.

Tests:

```sh
PATH=$PATH:/sbin go test ./...
```

Some tests run only when their tools or data are available:

- `ALTHEX_BINUTILS` points to GNU binutils.
- `ALTHEX_FW_SAMPLES` points to a directory of real firmware.

Other scripts:

- `scripts/fuzz.sh` runs the fuzzers.
- `scripts/startup-time.sh` measures the start-up time.

Settings are stored in `~/.altbins/.althex/`.

## License

[MIT](LICENSE) © Ivan Poluianov.

The notices of the software built into AltHex are in
[app/notices.txt](app/notices.txt); `althex licenses` prints them.
