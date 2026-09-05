# mcp-filesystem

Pure-Go MCP server for scoped local file access (Go standard library only).
All paths are symlink-resolved and validated against the allowed directories
passed as command-line arguments before any filesystem access.

## Run

```bash
go build -o mcp-filesystem .
./mcp-filesystem /home/user/projects /tmp   # allowed directories
```

Add to `~/.scorp/mcp.json`:

```json
{ "mcpServers": { "filesystem": { "command": "/path/to/mcp-filesystem", "args": ["/home/user/projects"] } } }
```

## Tools

`read_file`, `read_multiple_files`, `write_file`, `edit_file`, `create_directory`,
`list_directory`, `directory_tree`, `move_file`, `search_files`, `get_file_info`,
`list_allowed_directories` — mirroring the upstream filesystem server 1:1.

## Security

* Path boundaries enforced on every operation, including `move_file` source and
  destination and every entry of `read_multiple_files`.
* Symlink traversal is resolved before validation (`filepath.EvalSymlinks`).
* `directory_tree` is depth-capped (12) and skips `.git`.
* No shell execution anywhere; pure stdlib filesystem syscalls.

## Attribution

Port of the reference `filesystem` MCP server by Anthropic / MCP contributors
(TypeScript, MIT) — see `manifest.json` for the pinned upstream commit.
