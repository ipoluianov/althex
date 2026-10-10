# Changelog

## 1.0.0

The first release.

### Files and editing

- Files of any size open at once. Inserts and deletes do not rewrite the
  file, and undo is unlimited, with a history tree whose every state can be
  returned to.
- The edits are journaled, so they come back after a crash. Saving is
  atomic: blocks are written in place, or a temporary file is renamed over
  the original. A backup copy is optional.
- New documents can be zeros of a given size or made from the clipboard.
  Locked files open read-only. AltHex notices when a file changes on disk.
- **View:**
  - Bytes are shown as hex, binary, octal or decimal.
  - Rows are 8–64 bytes wide, or as wide as the window. Bytes can be
    grouped by 1–16, little- or big-endian.
  - Text can be shown in 23 encodings, with color rules for byte values.
  - A split view, and a minimap of the document.
- **Edit operations:**
  - Fill with a value, a pattern, random bytes or a counter.
  - Bit operations: AND, OR, XOR, NOT, shifts, rotations, add and subtract,
    reversing the byte order.
  - Copy as hex, a C/Go/Python/Rust array, Base64 or an escaped string.
  - Import and export Intel HEX, S-record, Base64 and code arrays.
- **Search:**
  - Hex patterns with wildcards, text in any encoding, typed numbers.
  - Lists of all results, which can be exported.
  - A strings extractor.
  - Several gigabytes per second on all cores.
- **Analysis:**
  - A data inspector: integers, floats, dates, GUIDs and LEB128.
  - Hashes and CRCs, including your own polynomial.
  - Entropy and a histogram.
- **Compare:** byte by byte or with shifts; documents need not be saved
  first. Reports, and IPS/BPS/AltHex/hex-diff patches.
- **Navigation:** go to an offset or an expression, back and forward,
  bookmarks with a name, color and comment (export and import), and
  offsets relative to a mark.
- A command palette with every command.

### Templates

- A C-like template language close to 010 Editor's:
  - structs, unions, bit fields, enums, arrays, functions;
  - read/write functions and checksums;
  - a console of expressions.
- The format is detected on open and its template is applied.
- Built-in templates:
  - images and media: BMP, PNG, JPEG/Exif, GIF, WAV, MP4;
  - archives and documents: ZIP/ZIP64, PDF;
  - executables: ELF, PE, Mach-O;
  - disk formats: MBR, GPT, APM, BSD, VTOC, FAT, exFAT, NTFS, ext2–4, HFS+,
    APFS, ISO 9660, UDF;
  - disk images: VHD, VHDX, VMDK, QCOW2, VDI, Android sparse;
  - boot images: Android boot, vendor_boot, uImage, FIT, cpio, bzImage, ARM
    zImage, El Torito, GRUB, boot sectors, syslinux;
  - firmware: UF2, DfuSe, ESP32 images.

### Disks and images

- **Disks** open read-only. Writing needs two confirmations that name the
  device and the ranges. Unreadable sectors are marked.
- **Disk images:** VHD, VHDX, VMDK, QCOW2, VDI and Android sparse open as
  flat disks.
- **Partition tables:**
  - MBR/EBR, GPT (with its backup), hybrid MBR, APM, BSD and VTOC;
  - CRC and overlap checks, with the problems highlighted;
  - a partition opens as its own document.
- **File systems:** FAT, exFAT, NTFS, ext2–4, ISO 9660 (Joliet, Rock
  Ridge) and UDF. Trees, file extraction, and going to a file's clusters.
- **Boot sectors** are disassembled as 16-bit x86, with BIOS calls named.
- **Boot images** (Android, U-Boot, initramfs, El Torito): their parts can
  be extracted, also decompressed.

### Firmware

- **Formats:** bin, Intel HEX, S-record, ELF, UF2, DfuSe, ESP32 and ESP8266
  images and full ESP flash dumps.
- **Detection:** the architecture and the load address are detected, and
  the analysis starts on open.
- **Disassemblers:** ARM Thumb/Thumb-2/A32 (v6-M to v8-M with FPU and DSP),
  ARM64, AVR (including XMEGA), RISC-V RV32/RV64 with C, Xtensa (LX6 and
  LX106), and x86 16/32/64.
- **Analysis:**
  - recursive disassembly that follows calls through registers, pointer
    tables and switch tables;
  - functions, cross-references, a call graph;
  - your names and comments, kept per file.
- **Peripheral registers:** 154 microcontrollers (STM32, nRF, RP2040/2350,
  SAM, LPC, Kinetis, ESP32, GD32V, CH32V, AVR). Register accesses are named
  and their bits decoded. Each device has its memory map and vector table.
- **Configuration areas decoded:** STM32 option bytes, AVR fuses, ESP32
  eFuse.
- **Export:** assembler source that GNU as builds back into the same bytes.
  This was checked on Arduino and MicroPython builds for ARM, AVR, RISC-V
  and Xtensa.

### Automation

- **Command line:** `hash` (with custom CRC), `find`, `replace`,
  `strings`, `diff`, `patch-make`, `patch-apply`, `parse`, `convert`,
  `licenses`.
- **Local JSON-RPC API** over a socket of the user or a named pipe on
  Windows.
- **MCP server** for AI agents, over stdio and HTTP. Edits from outside are
  asked about, become one undo step and are highlighted. All of this is off
  by default.

### Other

- The interface is in 12 languages: English, Russian, Polish, Serbian,
  German, French, Spanish, Italian, Portuguese, Chinese, Japanese and
  Korean.
- No telemetry; nothing goes to the network unless you ask.
