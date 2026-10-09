# AltHex

A hex editor for files of any size, disks and firmware images. It runs on
Linux, Windows and macOS as a single binary.

Website and documentation: https://altbins.pro/althex/

AltHex is at an early stage, see [PLAN.md](PLAN.md).

## Building

You need Go (see the version in [go.mod](go.mod)). No cgo is required.

```sh
go build .
```

`build/all.sh` (or `build/all.bat`) builds every platform into `bin/`,
together with the deb/rpm packages, the dmg and the installer script.

Settings are stored in `~/.altbins/.althex/`.

## License

[MIT](LICENSE) © Ivan Poluianov
