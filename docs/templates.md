# AltHex templates

A template describes the structure of binary data. AltHex runs it over a
document and shows the result as a tree: every field with its offset, size
and value, in sync with the hex view. The language is close to the Binary
Templates of 010 Editor: a template is a C-like program in which a
declaration of a variable **reads** it from the document at the current
position.

```c
//! name: Bitmap image
//! extensions: bmp
//! magic: 0 "42 4D"

LittleEndian();

struct FILEHEADER {
    char   type[2];
    uint32 size;
    uint16 reserved[2];
    uint32 offBits <format=hex>;
};

struct INFOHEADER {
    uint32 size;
    int32  width, height;
    uint16 planes, bitCount;
    uint32 compression <comment="0 - none, 1 - RLE8, 2 - RLE4, 3 - bitfields">;
};

FILEHEADER file;
INFOHEADER info;
if (info.bitCount <= 8)
    uint32 palette[1 << info.bitCount] <format=hex>;
FSeek(file.offBits);
uchar pixels[FileSize() - FTell()];
```

## Running

A template runs from the start of the document (or the selection, see
below) to its end. The fields it declares make the tree; what it prints
with `Printf` goes to the console. An error stops it with the line and
the field path; the fields read so far stay in the tree, which helps with
damaged files.

Templates run in a sandbox: they read and write only the document they
run on, have no access to files or the network, and are limited in time,
in the number of fields (1 000 000) and in the depth of calls and nested
structs (256).

## Comments and metadata

`//` and `/* */` comments. Lines starting with `//!` at the top of a
template tell AltHex about it:

| Key | Meaning |
| --- | --- |
| `name` | the name shown in the list of templates |
| `extensions` | file extensions, comma separated, to choose the template |
| `magic` | `OFFSET "HEX"`: bytes at an offset (negative - from the end), `??` for any byte; several `magic` lines mean any of them |
| `priority` | when several templates match, the highest wins (default 0) |

## Types

| Type | Size | Also written |
| --- | --- | --- |
| `char`, `int8` | 1 | `byte`, `CHAR` |
| `uchar`, `uint8` | 1 | `ubyte`, `UCHAR`, `BYTE` |
| `int16` | 2 | `short`, `SHORT` |
| `uint16` | 2 | `ushort`, `USHORT`, `WORD` |
| `int32` | 4 | `int`, `long`, `INT`, `LONG` |
| `uint32` | 4 | `uint`, `ulong`, `UINT`, `ULONG`, `DWORD` |
| `int64` | 8 | `quad`, `__int64`, `QUAD` |
| `uint64` | 8 | `uquad`, `__uint64`, `UQUAD`, `QWORD` |
| `float` | 4 | `FLOAT` |
| `double` | 8 | `DOUBLE` |
| `hfloat` | 2 | half precision |
| `string` | to the zero byte | `char` text, the zero included |
| `wstring` | to the zero char | UTF-16 text |

`typedef TYPE NAME;` gives a type another name; `typedef TYPE NAME[N];`
names an array, and an array of it is an array of arrays
(`typedef char FOURCC[4]; FOURCC names[8];`).

### Byte order

`LittleEndian()` and `BigEndian()` switch the order for the fields read
after them; the default is little-endian. `IsLittleEndian()` and
`IsBigEndian()` tell which it is.

### Arrays

`TYPE name[COUNT];` reads COUNT elements. An array of a simple type is
one field: its elements are read when they are looked at, so an array of
a billion bytes costs nothing. An array of `char` is shown as text.
An array of structs reads each element in turn.

Declaring a field with the name of a field of the same struct makes an
array of them: `while (!FEof()) CHUNK chunk;` gives `chunk[0]`,
`chunk[1]`... (a *repeated field*). A field declared only once can be
reached as `chunk[0]` too, and `chunk.len` after `chunk` repeats is the
field of the last one read. `startof(chunk)` of a repeated field is the
start of the first one (as in 010 Editor): to know where the one being read
starts, keep `FTell()` before its declaration.

### Structs and unions

```c
struct NAME (PARAMETERS) { DECLARATIONS AND STATEMENTS } VARIABLES;
union NAME { ... };
```

A struct is code: its body runs each time a field of it is read, so a
struct can decide what it contains from what it has read already. The
parameters are passed when a field is declared: `CHUNK c(16);`. All the
fields of a union start at the same offset; its size is that of the
largest one.

A field of a struct is reached with `.`: `file.size`, `chunk[2].type`.
Inside a struct the names of its fields, of the structs it is in and of
the template are seen without a prefix, the nearest first.

### Enums

```c
enum <uchar> COLOR { RED, GREEN = 5, BLUE };
COLOR c;
```

The type in angle brackets is the size of the value (`int` by default). A
field of an enum shows the name of its value.

### Bit fields

```c
uint16 type : 4;
uint16 flags : 12;
```

Bit fields of the same type follow each other in one value of that type;
a new value starts when it has no bits left or the type changes. In
little-endian the bits are taken from the lowest, in big-endian from the
highest; `BitfieldRightToLeft()` (from the lowest) and
`BitfieldLeftToRight()` (from the highest) set the order whatever the byte
order is, `BitfieldByteOrder()` goes back to the default.

### Placing a field at an offset

`TYPE name @ OFFSET;` reads the field at the offset without moving the
current position: the way to follow a pointer.

```c
uint32 tableOffset;
TABLE table @ tableOffset;
```

## Local variables

`local TYPE name = VALUE;` is a variable of the program, not read from
the document. `local` arrays (`local int counts[16];`) and strings
(`local string s = "x";`) can be made too.

## Statements and expressions

`if`/`else`, `while`, `do`/`while`, `for`, `switch`/`case`/`default`,
`break`, `continue`, `return`, blocks. The operators are those of C with
the same precedence: arithmetic, bitwise, logical, comparison, `?:`,
assignments (`=`, `+=`...) and `++`/`--` on local variables, casts like
`(uint16)x`, `sizeof(TYPE or field)`, `startof(field)`, `exists(field)`.
Integers are 64-bit; an operation with an unsigned value is unsigned.

Assigning to a field of the document writes the new value into it:
`header.crc = Checksum("crc32", 0, 100);`. A template that writes is
asked about before it runs, and its writes are one edit that undo takes
back at once.

## Attributes

After a declaration, in angle brackets:

| Attribute | Meaning |
| --- | --- |
| `format=hex` / `dec` / `oct` / `bin` | how a number is shown |
| `comment="text"` or `comment=Function` | a comment; the function gets the field and returns a string |
| `read=Function` | the function makes the value shown; on an array it is the value of the array, so for its elements put it on the struct |
| `name="text"` | the name shown instead of the variable's |
| `color=0xRRGGBB` | the background of the field in the hex view |
| `hidden=true` | not shown in the tree |
| `open=true` | expanded in the tree |

## Functions

```c
string ChunkComment(CHUNK &c) {
    return Str("%c%c%c%c", c.type[0], c.type[1], c.type[2], c.type[3]);
}
```

Parameters are values; `&` passes a field. A function may read the
document with the `Read...` functions and write it with the `Write...`
ones.

### Transforms and checksums

A function with the attribute `transform` or `checksum` is offered in the
menu when the template is loaded (requirement ANL-12):

```c
void XorKey(int64 start, int64 size) <transform="XOR with the key 5A">
{
    local int64 i;
    for (i = 0; i < size; i++)
        WriteUByte(start + i, ReadUByte(start + i) ^ 0x5A);
}

uint32 Sum32(int64 start, int64 size) <checksum="Sum of bytes (32)">
{
    local uint32 s = 0;
    local int64 i;
    for (i = 0; i < size; i++) s += ReadUByte(start + i);
    return s;
}
```

A transform gets the selection; its writes are one edit. A checksum gets
the selection and its result is shown.

## Built-in functions

| Function | |
| --- | --- |
| `FTell()`, `FSeek(pos)`, `FSkip(n)`, `FEof()`, `FileSize()` | the current position and the size |
| `LittleEndian()`, `BigEndian()`, `IsLittleEndian()`, `IsBigEndian()` | byte order |
| `ReadByte(pos)`, `ReadUByte`, `ReadShort`, `ReadUShort`, `ReadInt`, `ReadUInt`, `ReadInt64`, `ReadUInt64`, `ReadFloat`, `ReadDouble`, `ReadHFloat` | a value at an offset, the position stays |
| `ReadString(pos [, maxLen])`, `ReadWString(pos [, maxLen])` | text to the zero, at most maxLen bytes (characters for UTF-16) |
| `WriteByte(pos, v)`, `WriteUByte`, `WriteShort`, `WriteUShort`, `WriteInt`, `WriteUInt`, `WriteInt64`, `WriteUInt64`, `WriteFloat`, `WriteDouble`, `WriteString(pos, s)` | write a value |
| `InsertBytes(pos, n [, value])`, `DeleteBytes(pos, n)` | insert or remove bytes |
| `FindHex(hex, start [, end])` | the offset of the first match of hex bytes (`"FF D8 ??"`) from start, -1 if none |
| `Checksum(algorithm, start, size)` | `"crc32"`, `"md5"`, `"sha256"`...: a number for CRCs and Adler, hex text for the others |
| `GetSelStart()`, `GetSelSize()`, `GetCursorPos()` | the selection and the caret |
| `Printf(format, ...)`, `Str(format, ...)` | formatted output / text, `%d %u %x %X %o %c %s %f %e %g %%`; `%s` of a text field (`string`, an array of `char`) is its text, of another field the value the tree shows |
| `Warning(format, ...)`, `Assert(cond [, message])`, `Exit(code)` | messages, stop |
| `Strlen(s)`, `SubStr(s, start [, len])`, `Strstr(s, sub)`, `Atoi(s)`, `ToUpper(s)`, `ToLower(s)` | text |
| `Abs(x)`, `Min(a, b)`, `Max(a, b)`, `Pow(x, y)`, `Floor(x)`, `Ceil(x)` | numbers |
| `EnumToString(e)` | the name of an enum value |

## The expression console

The console evaluates an expression or a statement over the current
document: `ReadUInt(0x3C)`, `Checksum("sha256", GetSelStart(), GetSelSize())`,
`FileSize() / 512`. The fields of the last template run are seen by
their names (`file.size`), and the functions it declares can be called.
