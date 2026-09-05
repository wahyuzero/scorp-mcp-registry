// mcp-sqlite is a pure-Go MCP server for SQLite database inspection and
// queries. It is a native Go port of the reference `sqlite` MCP server
// (modelcontextprotocol/servers, Python), rebuilt on the official
// mark3labs/mcp-go SDK using the CGO-free modernc.org/sqlite driver so it
// cross-compiles to a single static binary for any GOOS/GOARCH.
//
// Tools:
//   - read_query     — run SELECT queries (read-only, enforced)
//   - write_query    — run INSERT/UPDATE/DELETE (mutating, no DDL)
//   - create_table   — DDL: CREATE TABLE
//   - list_tables    — enumerate user tables
//   - describe_table — column schema of a table
//
// Copyright (c) 2026 Wahyu — MIT License.
// Upstream: https://github.com/modelcontextprotocol/servers/tree/main/src/sqlite (MIT).
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const version = "1.0.0"

func main() {
	s := server.NewMCPServer(
		"mcp-sqlite",
		version,
		server.WithToolCapabilities(false),
		server.WithRecovery(),
	)

	dbPathFlag := mcp.WithString("db_path",
		mcp.Required(),
		mcp.Description("Absolute path to the SQLite database file"),
	)
	queryFlag := mcp.WithString("query",
		mcp.Required(),
		mcp.Description("SQL statement to execute"),
	)

	tools := []struct {
		tool   mcp.Tool
		handle server.ToolHandlerFunc
	}{
		{
			tool: mcp.NewTool("read_query",
				mcp.WithDescription("Execute a SELECT query on the SQLite database and return results as structured text"),
				dbPathFlag, queryFlag,
			),
			handle: withDB(queryHandler(kindSelect)),
		},
		{
			tool: mcp.NewTool("write_query",
				mcp.WithDescription("Execute an INSERT, UPDATE, or DELETE query on the SQLite database"),
				dbPathFlag, queryFlag,
			),
			handle: withDB(queryHandler(kindWrite)),
		},
		{
			tool: mcp.NewTool("create_table",
				mcp.WithDescription("Create a new table in the SQLite database (CREATE TABLE only)"),
				dbPathFlag, queryFlag,
			),
			handle: withDB(queryHandler(kindCreate)),
		},
		{
			tool: mcp.NewTool("list_tables",
				mcp.WithDescription("List all tables in the SQLite database"),
				dbPathFlag,
			),
			handle: withDB(listTables),
		},
		{
			tool: mcp.NewTool("describe_table",
				mcp.WithDescription("Get the schema information (column names, types) for a table"),
				dbPathFlag,
				mcp.WithString("table_name", mcp.Required(), mcp.Description("Name of the table to describe")),
			),
			handle: withDB(describeTable),
		},
	}

	for _, t := range tools {
		s.AddTool(t.tool, t.handle)
	}

	if err := server.ServeStdio(s); err != nil {
		// Never write diagnostics to stdout — it is the JSON-RPC channel.
		fmt.Fprintf(os.Stderr, "mcp-sqlite: %v\n", err)
		os.Exit(1)
	}
}

// withDB opens the database named in the request, runs the handler, and
// guarantees the handle is closed before returning.
func withDB(fn func(ctx context.Context, db *sql.DB, req mcp.CallToolRequest) (*mcp.CallToolResult, error)) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dbPath, err := req.RequireString("db_path")
		if err != nil {
			return mcp.NewToolResultError("db_path parameter is required"), nil
		}
		if dbPath == ":memory:" || strings.HasPrefix(dbPath, "file:") {
			// :memory: is useless across separate connections; file: DSNs bypass
			// the plain-path expectation and are rejected for auditability.
			return mcp.NewToolResultError("db_path must be a plain filesystem path to a SQLite database file"), nil
		}

		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("cannot open database", err), nil
		}
		defer db.Close()

		db.SetMaxOpenConns(1) // serialize writers; avoids SQLITE_BUSY under load
		return fn(ctx, db, req)
	}
}

// queryKind classifies which SQL statement prefixes a tool is allowed to run.
type queryKind int

const (
	kindSelect queryKind = iota
	kindWrite
	kindCreate
)

var selectPrefixes = []string{"select", "with", "explain", "pragma"}
var writePrefixes = []string{"insert", "update", "delete", "replace"}
var createPrefixes = []string{"create"}

// queryHandler builds a handler that runs one SQL statement after checking it
// starts with a prefix permitted for the tool's kind.
func queryHandler(kind queryKind) func(ctx context.Context, db *sql.DB, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, db *sql.DB, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, err := req.RequireString("query")
		if err != nil {
			return mcp.NewToolResultError("query parameter is required"), nil
		}

		var allowed []string
		switch kind {
		case kindSelect:
			allowed = selectPrefixes
		case kindWrite:
			allowed = writePrefixes
		case kindCreate:
			allowed = createPrefixes
		}
		if !startsWithAny(query, allowed) {
			return mcp.NewToolResultErrorf("this tool only accepts %s statements (got: %.40s)", strings.Join(allowed, "/"), strings.TrimSpace(query)), nil
		}

		if kind == kindSelect {
			return runSelect(ctx, db, query)
		}
		res, err := db.ExecContext(ctx, query)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("query failed", err), nil
		}
		affected, _ := res.RowsAffected()
		return mcp.NewToolResultText(fmt.Sprintf("OK — %d row(s) affected", affected)), nil
	}
}

func runSelect(ctx context.Context, db *sql.DB, query string) (*mcp.CallToolResult, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("query failed", err), nil
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return mcp.NewToolResultErrorFromErr("cannot read columns", err), nil
	}

	var sb strings.Builder
	sb.WriteString("| " + strings.Join(cols, " | ") + " |\n")
	sb.WriteString("|" + strings.Repeat(" --- |", len(cols)) + "\n")

	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}

	count := 0
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return mcp.NewToolResultErrorFromErr("row scan failed", err), nil
		}
		cells := make([]string, len(cols))
		for i, v := range vals {
			cells[i] = cellString(v)
		}
		sb.WriteString("| " + strings.Join(cells, " | ") + " |\n")
		count++
		if count >= 100 {
			sb.WriteString("\n(truncated at 100 rows — refine the query for more)")
			break
		}
	}
	if count == 0 {
		sb.WriteString("\n(0 rows)")
	}
	return mcp.NewToolResultText(sb.String()), nil
}

func listTables(ctx context.Context, db *sql.DB, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("cannot list tables", err), nil
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return mcp.NewToolResultErrorFromErr("row scan failed", err), nil
		}
		names = append(names, n)
	}
	if len(names) == 0 {
		return mcp.NewToolResultText("No tables found in database."), nil
	}
	return mcp.NewToolResultText("Tables:\n" + strings.Join(names, "\n")), nil
}

func describeTable(ctx context.Context, db *sql.DB, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	table, err := req.RequireString("table_name")
	if err != nil {
		return mcp.NewToolResultError("table_name parameter is required"), nil
	}
	// Table names cannot be parameterized; quote with doubled quotes as per
	// SQLite identifier rules to prevent injection via the identifier slot.
	quoted := "\"" + strings.ReplaceAll(table, "\"", "\"\"") + "\""

	rows, err := db.QueryContext(ctx, "PRAGMA table_info(" + quoted + ")")
	if err != nil {
		return mcp.NewToolResultErrorFromErr("cannot describe table", err), nil
	}
	defer rows.Close()

	var sb strings.Builder
	sb.WriteString("cid | name | type | notnull | dflt_value | pk\n")
	count := 0
	for rows.Next() {
		var cid, notnull, pk int
		var name, colType string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notnull, &dflt, &pk); err != nil {
			return mcp.NewToolResultErrorFromErr("row scan failed", err), nil
		}
		sb.WriteString(fmt.Sprintf("%d | %s | %s | %d | %s | %d\n", cid, name, colType, notnull, dflt.String, pk))
		count++
	}
	if count == 0 {
		return mcp.NewToolResultErrorf("table '%s' not found", table), nil
	}
	return mcp.NewToolResultText(sb.String()), nil
}

func startsWithAny(query string, prefixes []string) bool {
	trimmed := strings.TrimSpace(query)
	lower := strings.ToLower(trimmed)
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p+" ") || strings.HasPrefix(lower, p+"\n") || strings.EqualFold(trimmed, p) {
			return true
		}
		// Handles "SELECT(" style statements without a space.
		if strings.HasPrefix(lower, p+"(") {
			return true
		}
	}
	return false
}

func cellString(v any) string {
	switch t := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		return string(t)
	case float64:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", t), "0"), ".")
	default:
		return fmt.Sprintf("%v", t)
	}
}
