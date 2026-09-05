# mcp-sqlite

Pure-Go MCP server for SQLite inspection and queries. Uses the CGO-free
`modernc.org/sqlite` driver so it builds as a static binary for any
GOOS/GOARCH. Query-kind enforcement keeps each tool on its contract:
`read_query` only runs SELECT/WITH/EXPLAIN/PRAGMA, `write_query` only
INSERT/UPDATE/DELETE/REPLACE, `create_table` only CREATE.

## Tools

| Tool | Params |
| :--- | :--- |
| `read_query` | `db_path`, `query` (SELECT) |
| `write_query` | `db_path`, `query` (INSERT/UPDATE/DELETE) |
| `create_table` | `db_path`, `query` (CREATE TABLE) |
| `list_tables` | `db_path` |
| `describe_table` | `db_path`, `table_name` |

SELECT results are capped at 100 rows per call.

## Run

```bash
go build -o mcp-sqlite .
```

Add to `~/.scorp/mcp.json`:

```json
{ "mcpServers": { "sqlite": { "command": "/path/to/mcp-sqlite" } } }
```

## Attribution

Port of the reference `sqlite` MCP server by Anthropic / MCP contributors
(Python, MIT) — see `manifest.json` for the pinned upstream commit.
