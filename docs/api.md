# External interfaces: MCP and the local API

AltHex has no plugins and no scripting language of its own (EXT-1, EXT-4).
Other programs and AI agents work with it from outside, over open protocols:

- **MCP** (Model Context Protocol), for AI agents: over stdio (`althex mcp`)
  or over HTTP;
- **the local API**, JSON-RPC 2.0 for programs: over stdio (`althex rpc`),
  a unix socket, or a named pipe on Windows.

Both have the same operations ("tools"), listed below. A tool that changes
anything asks the user first.

## Turning them on

The interfaces are off by default. Turn them on in **Options → External
interfaces**:

- **Local API.** The window listens on a socket only the user can reach:
  - Linux: `$XDG_RUNTIME_DIR/althex-api-<hash>.sock`;
  - macOS: `~/.altbins/.althex/run/api.sock`;
  - Windows: the pipe `\\.\pipe\althex-api-<user>`.
- **MCP over HTTP.** The window listens on `http://127.0.0.1:<port>/mcp`
  (port 47321 by default). Clients must send `Authorization: Bearer <token>`;
  the token is in the same dialog. Requests from web pages (an `Origin` other
  than localhost) are refused.
- **Apply the edits of clients without asking.** Off by default; leave it
  off unless you trust every client.

## Connecting an AI agent

An agent starts `althex mcp` and talks to it over stdin and stdout. With the
window running and the local API on, the calls go to the window: the agent
sees the documents open there, and the user is asked about changes. Without
the window, the command works on the files it is given:

| Command | What the agent can do |
| --- | --- |
| `althex mcp` | the documents of the window (local API on) |
| `althex mcp FILE...` | without the window: read these files |
| `althex mcp -allow-open FILE...` | ... and open other files |
| `althex mcp -allow-write FILE...` | ... and edit and save them |
| `althex mcp -headless ...` | never use the window, even when it runs |
| `althex mcp -http 127.0.0.1:PORT -token T FILE...` | serve MCP over HTTP without the window |

For example, in Claude Code:

```
claude mcp add althex -- althex mcp
```

For a client that connects over HTTP, use the URL
`http://127.0.0.1:47321/mcp` and the header
`Authorization: Bearer <token>`.

## The local API

The local API is JSON-RPC 2.0, one message per line (batches allowed).
Connect to the socket or pipe of the window, or start `althex rpc`, which
works the same way as `althex mcp`.

The optional first call introduces the client by name; the name is shown to
the user:

```json
{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"clientInfo": {"name": "my-script", "version": "1"}}}
{"jsonrpc": "2.0", "id": 2, "method": "read", "params": {"offset": 0, "length": 16, "format": "hex"}}
```

A method is a tool, and its params are the tool's arguments. The result is
an object. The errors are:

| Code | Meaning |
| --- | --- |
| -32601 | no such method |
| -32602 | wrong parameters |
| -32001 | the user declined |
| -32002 | not found: a document, a template, a field |
| -32003 | not allowed: switched off, read-only, a disk |
| -32004 | too large for one call |

Over MCP, these errors come back as tool results with `isError: true`.

## Tools

A document is named by the `id` that `list_documents` returns. If
`document` is absent or empty, the tool works on the document the user is
looking at. Offsets and sizes are decimal byte counts.

| Tool | Arguments | Result |
| --- | --- | --- |
| `list_documents` | — | id, name, path, size, modified, read_only, current, caret, selection |
| `open_document` | `path` | the document; the user is asked |
| `read` | `offset`, `length` (256), `format`: `dump` (≤ 64 KB), `hex`, `base64`, `text` (≤ 1 MB), `encoding` | the bytes; the unreadable sectors of disks |
| `search` | `hex` (`??` and `4?` wildcards) or `text` with `encoding`, `ignore_case`; `from`, `to`, `align`, `max_results` (100, ≤ 10000) | the offsets of the matches |
| `hash` | `algorithms` (crc32, md5, sha256 by default; crc8…crc64, adler32, xor8, sum8/16/32, sha1, sha512, blake3…), `offset`, `length` | the hex digests |
| `list_templates` | `document` | the templates; the one that fits the document |
| `apply_template` | `template` (empty: the one that fits), `start` | number of fields, warnings, errors, output, the top of the tree |
| `get_structure` | `path` (`ehdr.e_entry`, `sections[2]`) or `offset`, `depth` (2, ≤ 8), `max_children` (100, ≤ 1000) | the fields: name, path, type, offset, size, value, comment, children |
| `propose_edits` | `edits`: `{offset, overwrite: hex}`, `{offset, insert: hex}`, `{offset, delete: n}`; `description` | the document after the edits; the user is asked |
| `save_document` | — | the document; the user is asked |
| `show` | `offset`, `length` | selects the bytes in the window |

How `propose_edits` works:

- The edits apply in order. The offsets of an edit refer to the document as
  the edits before it left it.
- The user sees the list of edits and the description. If the user agrees,
  the edits become one step of the undo history and are highlighted until
  the document changes again.
- The edits are not saved to the file. `save_document` asks the user
  separately.
- If one edit fails, none of the proposal stays.
- If the client disconnects or nobody answers within five minutes, the
  proposal is declined.

Templates applied over the API do not write to the document.
