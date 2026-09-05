# mcp-fetch

Pure-Go MCP server: fetches a web URL and extracts its content as markdown,
using a Firefox Reader-style extraction engine (go-readability) followed by
HTML→markdown conversion. Single static binary, ~5MB RAM.

## Tool

| Tool | Params |
| :--- | :--- |
| `fetch` | `url` (required), `max_length` (default 20000), `start_index` (pagination), `raw` (return raw HTML) |

## Run

```bash
go build -o mcp-fetch .
./mcp-fetch   # speaks JSON-RPC 2.0 over stdio
```

Add to `~/.scorp/mcp.json`:

```json
{ "mcpServers": { "fetch": { "command": "/path/to/mcp-fetch" } } }
```

## Attribution

Port of the reference `fetch` MCP server by Anthropic / MCP contributors
(TypeScript, MIT) — see `manifest.json` for the pinned upstream commit.
