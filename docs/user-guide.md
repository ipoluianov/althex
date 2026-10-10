# AltHex user guide

AltHex is a hex editor for files of any size, disks, disk images and
firmware. It runs on Windows, macOS and Linux and ships as one file.

## Quick start

- **Open a file.** Use **File → Open** (<kbd>Ctrl+O</kbd>), drag the file
  onto the window, or pass it on the command line: `althex FILE`. Files of
  any size open at once, because AltHex reads only the parts you look at.
  With no document open, the window offers **Open** and the recent files.
- **Edit the bytes.** Type in the hex column or in the text column. In
  overwrite mode (the default) typing replaces bytes. In insert mode
  (<kbd>Ins</kbd>) typing inserts them. The status bar shows the current
  mode.
- **Save.** Use <kbd>Ctrl+S</kbd>. Until you save, the file on disk stays
  as it was. Undo (<kbd>Ctrl+Z</kbd>) and redo (<kbd>Ctrl+Y</kbd>) have no
  limit.
- **Find any command.** The command palette (<kbd>Ctrl+Shift+P</kbd>) lists
  every command, with its shortcut; the commands of a submenu carry its
  name, as in **Copy as: Go**.

## Common tasks

### Change text in a 50 GB file

1. Open the file. It opens in a moment, whatever its size.
2. Press <kbd>Ctrl+G</kbd> and enter an offset. Hex as the view shows it
   (`8A3D`) works, and so does an expression such as `0x400 + 3*512`, or
   `+16` to move relative to the caret. Digits alone, such as `1000`, are
   decimal.
3. Press <kbd>Ctrl+H</kbd> to open **Replace**. Choose text or hex, the
   encoding and the case, then **Replace** or **Replace all**. In the
   search field, <kbd>Enter</kbd> finds the next match and
   <kbd>Shift+Enter</kbd> the previous one; the panel shows which match of
   how many it is.
4. Press <kbd>Ctrl+S</kbd>. If the size did not change, only the changed
   blocks are written. If you inserted or deleted bytes, AltHex writes a
   temporary file and swaps it in at once.

### See the structure of PNG, ELF, PE, ZIP and other formats

Open the file. AltHex recognizes the format and opens the **Structure**
panel (<kbd>F6</kbd>) with the field names and values.

- Click a field to select its bytes.
- Move the caret to see which field it is in.
- Edit a value right in the tree: double-click it or press <kbd>F2</kbd>,
  then type a number (`0x1F`, `-3`, `1.5`) or a name of the enum. The edit
  is one undo step, and the tree shows the new value.
- To apply a template by hand, use **View → Apply the template**
  (<kbd>F5</kbd>) and choose one.

The built-in templates cover many formats:

| Group | Formats |
| --- | --- |
| Images and media | BMP, PNG, JPEG with Exif, GIF, WAV, MP4 |
| Archives and documents | ZIP, PDF |
| Executables | ELF, PE, Mach-O |
| Disks | MBR, GPT, FAT, exFAT, NTFS, ext4, ISO 9660, UDF and other disk formats |
| Firmware | UF2, DfuSe, ESP32 images and other firmware formats |

You can also write your own templates; see
[the template language](templates.md).

### Compare two versions of a file and make a patch

1. Open the newer file and choose how to compare:
   - **Compare → Compare with a file** compares it with one or more files;
   - **Compare with open documents** compares it with other tabs (several
     can be chosen);
   - **Compare with shifts** is for two documents with inserted or deleted
     bytes.

   Unsaved edits take part as they are.
2. The documents are shown together and scroll together. <kbd>F8</kbd>
   and <kbd>Shift+F8</kbd> go to the next and previous difference. A long
   comparison can be canceled with the **Cancel** link in the status bar.
3. To save the list of differences, use **Compare → Export the report**.
4. To make a patch, use **Compare → Make a patch** and choose IPS, BPS,
   AltHex (`.ahp`) or a text hex diff.
5. To apply a patch to another copy of the file, use **Compare → Apply a
   patch**.

### Compute CRC32, SHA-256 and entropy of a range

1. Select the bytes. If nothing is selected, the whole file is used.
2. Open the **Hashes** panel (<kbd>Ctrl+Shift+H</kbd>) and tick the
   algorithms.
3. The panel also shows the entropy and the histogram of the byte values.

The algorithms are CRC-8, CRC-16 and CRC-32 in their common variants, CRC-64,
Adler-32, sums and XOR, MD5, SHA-1, SHA-2 and BLAKE3. For a CRC with your
own polynomial, set its parameters in the same panel.

### Find a 32-bit number in all open files

1. Press <kbd>Ctrl+F</kbd> and choose **Number**.
2. Pick the type (int8 to int64, float or double), the byte order and the
   value.
3. Choose **All open documents**, then **Find all**.

The results list shows every match; click one to go there. You can export
the list.

### Open a disk and save a piece of it

1. Use **File → Open a disk**. On Linux and macOS this needs
   administrator rights; on Windows, run AltHex as administrator.
2. The disk opens read-only. Its partitions, file systems and boot sectors
   are shown in the side panel. The minimap on the right shows the
   partitions:
   - point at a partition to see its name;
   - double-click it to go to it;
   - right-click it to open the partition as its own document.

   The file systems with a file tree are FAT, exFAT, NTFS, ext2–4, ISO 9660
   and UDF.
3. Select the range and choose **File → Export** to save it to a file. In
   the **Files** panel you can also extract a file from a partition.

Writing to a disk needs two confirmations that name the device and the
range. Sectors that cannot be read are shown as `??` and do not stop the
work.

### Fix headers in many files at once

Use the command line:

```
althex replace -hex "7F 45 4C 46 01" -with-hex "7F 45 4C 46 02" *.bin
althex hash -a crc32,sha256 *.bin
althex patch-apply fix.ips old.bin new.bin
```

For a CRC of your own, use `althex hash -crc "width=16 poly=0x1021
init=0xFFFF refin=false refout=false xorout=0" FILE`. Run `althex help` to
see every command. A program can also use
[the local API](api.md).

### Look into STM32, AVR or ESP32 firmware

Open the firmware: `.bin`, Intel HEX, S-record, ELF, UF2, DfuSe or an ESP
image. AltHex detects the architecture and the load address, analyzes the
code and shows the following:

- **the listing** next to the hex view, synchronized with it;
- **the Firmware panel**:
  - the device, chosen automatically or by you, and its memory map
    (Flash, SRAM, peripherals): double-click a region to go to it;
  - the vector table: click a vector to go to its handler;
  - the configuration areas decoded: option bytes, fuses, eFuse;
- **the Functions panel**, with the references to each function and a call
  tree of callees or callers; **Export call graph** writes a Graphviz `.dot`
  file.

Register accesses are named, for example
`str r1, [r0, #24] ; RCC->APB2ENR = IOPAEN=1 | IOPCEN=1`.

In the listing:

| Key | Action |
| --- | --- |
| <kbd>N</kbd> | rename |
| <kbd>;</kbd> | comment |
| <kbd>C</kbd> | mark as code |
| <kbd>Enter</kbd> | go to the target |

Names and comments are saved per file. **Export .s** writes an assembler
source; for ARM Thumb and AVR, GNU `as` rebuilds the same bytes from it.

The disassembler handles ARM (Thumb, Thumb-2, A32), ARM64, AVR, RISC-V,
Xtensa and x86.

## The window

| Area | What is there |
| --- | --- |
| Tabs | one per document; a split view (<kbd>Ctrl+\\</kbd>) shows two places of one document |
| Hex view | offsets, bytes and text; set the width, grouping, byte order, number base, encoding and color rules in the **View** menu |
| Side panel | data inspector (<kbd>F4</kbd>), structure (<kbd>F6</kbd>), console (<kbd>Ctrl+`</kbd>), bookmarks (<kbd>Ctrl+Shift+B</kbd>), hashes (<kbd>Ctrl+Shift+H</kbd>), history, search results, firmware, disks |
| Minimap | the whole document in a strip: the part on screen, edits, search hits, bookmarks, partitions, and how varied the bytes are; turn it on or off in **View → Minimap** |
| Status bar | offset, selection, value under the caret, insert/overwrite mode, encoding, byte order, read-only |

The **data inspector** shows the bytes at the caret as every integer type,
float and double, dates (Unix, FILETIME, DOS) and GUIDs, in both byte
orders. You can edit a value there.

### Editing

- Cut, copy and paste work as in a text editor. **Edit → Copy as** gives
  hex with or without spaces, an array for C, Go, Python or Rust, Base64,
  or an escaped string.
- **Fill** (<kbd>Ctrl+Shift+F</kbd>) writes a value, a pattern, random
  bytes or a counter.
- The bit operations act on the selection:
  - AND, OR, XOR, NOT;
  - shifts and rotations;
  - add and subtract;
  - reversing the byte order.
- **Read-only** protects a document from accidental edits. A file you
  cannot write (no permission, or locked by another program) opens
  read-only by itself.
- The **History** panel shows every state of the document as a tree. An
  undo followed by a new edit starts a branch, and nothing is lost.
  Double-click a state to return to it.

### Navigation

| Action | Key |
| --- | --- |
| Back and forward through the places you jumped from | <kbd>Alt+Left</kbd> / <kbd>Alt+Right</kbd> |
| Add or remove a bookmark | <kbd>Ctrl+B</kbd> |
| Next and previous bookmark | <kbd>F2</kbd> / <kbd>Shift+F2</kbd> |
| Count offsets from a mark | <kbd>Ctrl+M</kbd> sets the mark; turn on **View → From the mark** |

Bookmarks have a name, a color and a comment, and you can export and import
them. In the **Bookmarks** panel:
- edit the name, color or comment with <kbd>F2</kbd>; a color is written as
  `#rrggbb`;
- **Color** picks the next of the offered colors.

### Import and export

**File → Import** and **File → Export** convert Intel HEX, Motorola S-record,
Base64 and code arrays.

### Recovery

AltHex writes your edits to a journal as you work. If it closes without
saving, it offers to bring the edits back the next time it starts. If the
file changes on disk while it is open, AltHex offers to reload it. If you
have unsaved edits, it warns that reloading loses them.

### Boot images

The **Boot** panel lists the parts of Android boot images, U-Boot images,
initramfs and El Torito. **Extract decompressed** and **Open decompressed**
unpack gzip, zstd, LZ4, xz, lzma and bzip2 parts; a decompressed ramdisk
shows its files.

## Settings

Use **Options → Settings** for:

- the language (12 languages);
- the dark or light theme;
- a backup copy on save;
- always on top;
- the color rules of the bytes.

The settings are kept in `~/.altbins/.althex/`.

**Options → External interfaces** turns on the local API and MCP for other
programs and AI agents; see [the external interfaces](api.md). They are off
by default.

## Keys

| Key | Command |
| --- | --- |
| <kbd>Ctrl+N</kbd> / <kbd>Ctrl+O</kbd> / <kbd>Ctrl+S</kbd> / <kbd>Ctrl+Shift+S</kbd> | New, Open, Save, Save as |
| <kbd>Ctrl+W</kbd> / <kbd>Ctrl+Shift+W</kbd> | Close, Close all |
| <kbd>Ctrl+Z</kbd> / <kbd>Ctrl+Y</kbd> | Undo, Redo |
| <kbd>Ins</kbd> | Insert / overwrite |
| <kbd>Ctrl+F</kbd> / <kbd>Ctrl+H</kbd> / <kbd>F3</kbd> / <kbd>Shift+F3</kbd> | Find, Replace, Find next / previous |
| <kbd>Ctrl+G</kbd> | Go to offset |
| <kbd>Ctrl+B</kbd> / <kbd>F2</kbd> / <kbd>Shift+F2</kbd> | Bookmark, next / previous |
| <kbd>Alt+Left</kbd> / <kbd>Alt+Right</kbd> | Back / forward |
| <kbd>F4</kbd> / <kbd>F6</kbd> / <kbd>F5</kbd> | Data inspector, Structure, Apply the template |
| <kbd>Ctrl+Shift+H</kbd> / <kbd>Ctrl+Shift+B</kbd> / <kbd>Ctrl+`</kbd> | Hashes, Bookmarks, Console |
| <kbd>F8</kbd> / <kbd>Shift+F8</kbd> | Next / previous difference |
| <kbd>Ctrl+\\</kbd> | Split view |
| <kbd>Ctrl+=</kbd> / <kbd>Ctrl+-</kbd> / <kbd>Ctrl+0</kbd> | Zoom in, out, normal size |
| <kbd>Ctrl+Tab</kbd> | Next tab |
| <kbd>Ctrl+Shift+P</kbd> | Command palette |
| <kbd>F1</kbd> | This guide |

## Licenses

AltHex is distributed under the MIT License. **Help → About → License** and
`althex licenses` show the licenses of the parts built into it.
