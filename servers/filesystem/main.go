// mcp-filesystem is a pure-Go MCP server for scoped local file access.
// It is a native Go port of the reference `filesystem` MCP server
// (modelcontextprotocol/servers, TypeScript), rebuilt on the official
// mark3labs/mcp-go SDK using only the Go standard library.
//
// Security model: every operation is confined to the allowed directories
// passed as command-line arguments. Paths are resolved through symlinks and
// validated against those boundaries before any filesystem access (Layer 4
// path boundary enforcement of the Scorp zero-trust shield).
//
// Tools:
//   read_file, read_multiple_files, write_file, edit_file, create_directory,
//   list_directory, directory_tree, move_file, search_files, get_file_info,
//   list_allowed_directories
//
// Copyright (c) 2026 Wahyu — MIT License.
// Upstream: https://github.com/modelcontextprotocol/servers/tree/main/src/filesystem (MIT).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const version = "1.0.0"

var allowedDirs []string

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "mcp-filesystem: usage: mcp-filesystem <allowed-dir> [allowed-dir...]\n")
		os.Exit(1)
	}
	for _, dir := range os.Args[1:] {
		abs, err := filepath.Abs(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mcp-filesystem: invalid directory %q: %v\n", dir, err)
			os.Exit(1)
		}
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			if os.IsNotExist(err) {
				// Permit directories that do not exist yet; validation at call
				// time re-resolves and enforces the boundary.
				resolved = abs
			} else {
				fmt.Fprintf(os.Stderr, "mcp-filesystem: cannot resolve %q: %v\n", dir, err)
				os.Exit(1)
			}
		}
		allowedDirs = append(allowedDirs, resolved)
	}

	s := server.NewMCPServer(
		"mcp-filesystem",
		version,
		server.WithToolCapabilities(false),
		server.WithRecovery(),
	)

	pathFlag := mcp.WithString("path", mcp.Required(), mcp.Description("Filesystem path (must be inside an allowed directory)"))

	add := func(tool mcp.Tool, h server.ToolHandlerFunc) { s.AddTool(tool, h) }

	add(mcp.NewTool("read_file",
		mcp.WithDescription("Read the complete contents of a file as text (UTF-8)"),
		pathFlag,
	), guard(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		p, err := req.RequireString("path")
		if err != nil {
			return mcp.NewToolResultError("path parameter is required"), nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("read failed", err), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}))

	add(mcp.NewTool("read_multiple_files",
		mcp.WithDescription("Read the contents of multiple files simultaneously"),
		mcp.WithArray("paths", mcp.Required(), mcp.Description("Paths to read"), mcp.WithStringItems()),
	), guard(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		paths, err := req.RequireStringSlice("paths")
		if err != nil || len(paths) == 0 {
			return mcp.NewToolResultError("paths parameter is required"), nil
		}
		var sb strings.Builder
		for _, p := range paths {
			sb.WriteString("--- " + p + " ---\n")
			data, err := os.ReadFile(p)
			if err != nil {
				sb.WriteString("<error: " + err.Error() + ">\n")
				continue
			}
			sb.Write(data)
			sb.WriteString("\n")
		}
		return mcp.NewToolResultText(sb.String()), nil
	}))

	add(mcp.NewTool("write_file",
		mcp.WithDescription("Create a new file or overwrite an existing file with the provided contents"),
		pathFlag,
		mcp.WithString("content", mcp.Required(), mcp.Description("Full contents to write")),
	), guard(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		p, err := req.RequireString("path")
		if err != nil {
			return mcp.NewToolResultError("path parameter is required"), nil
		}
		content, err := req.RequireString("content")
		if err != nil {
			return mcp.NewToolResultError("content parameter is required"), nil
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return mcp.NewToolResultErrorFromErr("mkdir failed", err), nil
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return mcp.NewToolResultErrorFromErr("write failed", err), nil
		}
		st, _ := os.Stat(p)
		return mcp.NewToolResultText(fmt.Sprintf("Wrote %d bytes to %s", st.Size(), p)), nil
	}))

	add(mcp.NewTool("edit_file",
		mcp.WithDescription("Perform exact string replacement in a file (all occurrences unless expected_replacements is set)"),
		pathFlag,
		mcp.WithString("old_text", mcp.Required(), mcp.Description("Exact text to search for")),
		mcp.WithString("new_text", mcp.Required(), mcp.Description("Replacement text")),
	), guard(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		p, oldText, newText, err := requireThree(req, "path", "old_text", "new_text")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("read failed", err), nil
		}
		content := string(data)
		if !strings.Contains(content, oldText) {
			return mcp.NewToolResultError("old_text not found in file"), nil
		}
		updated := strings.ReplaceAll(content, oldText, newText)
		if err := os.WriteFile(p, []byte(updated), 0o644); err != nil {
			return mcp.NewToolResultErrorFromErr("write failed", err), nil
		}
		replacements := strings.Count(content, oldText)
		return mcp.NewToolResultText(fmt.Sprintf("Replaced %d occurrence(s) in %s", replacements, p)), nil
	}))

	add(mcp.NewTool("create_directory",
		mcp.WithDescription("Create a new directory including any necessary parents"),
		pathFlag,
	), guard(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		p, err := req.RequireString("path")
		if err != nil {
			return mcp.NewToolResultError("path parameter is required"), nil
		}
		if err := os.MkdirAll(p, 0o755); err != nil {
			return mcp.NewToolResultErrorFromErr("mkdir failed", err), nil
		}
		return mcp.NewToolResultText("Created directory " + p), nil
	}))

	add(mcp.NewTool("list_directory",
		mcp.WithDescription("List files and directories in a given path"),
		pathFlag,
	), guard(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		p, err := req.RequireString("path")
		if err != nil {
			return mcp.NewToolResultError("path parameter is required"), nil
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("list failed", err), nil
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		var sb strings.Builder
		for _, e := range entries {
			marker := "[FILE]"
			if e.IsDir() {
				marker = "[DIR]"
			}
			sb.WriteString(marker + " " + e.Name() + "\n")
		}
		return mcp.NewToolResultText(sb.String()), nil
	}))

	add(mcp.NewTool("directory_tree",
		mcp.WithDescription("Get a recursive JSON tree of the directory structure"),
		pathFlag,
	), guard(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		p, err := req.RequireString("path")
		if err != nil {
			return mcp.NewToolResultError("path parameter is required"), nil
		}
		tree, err := buildTree(p, 0)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("tree failed", err), nil
		}
		out, err := json.MarshalIndent(tree, "", "  ")
		if err != nil {
			return mcp.NewToolResultErrorFromErr("encode failed", err), nil
		}
		return mcp.NewToolResultText(string(out)), nil
	}))

	add(mcp.NewTool("move_file",
		mcp.WithDescription("Move or rename a file or directory"),
		mcp.WithString("source", mcp.Required(), mcp.Description("Source path")),
		mcp.WithString("destination", mcp.Required(), mcp.Description("Destination path")),
	), guard(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		src, dst, err := requireTwo(req, "source", "destination")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if err := os.Rename(src, dst); err != nil {
			return mcp.NewToolResultErrorFromErr("move failed", err), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Moved %s to %s", src, dst)), nil
	}))

	add(mcp.NewTool("search_files",
		mcp.WithDescription("Recursively search for files and directories matching a pattern (substring match)"),
		pathFlag,
		mcp.WithString("pattern", mcp.Required(), mcp.Description("Substring to match against names")),
	), guard(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		root, err := req.RequireString("path")
		if err != nil {
			return mcp.NewToolResultError("path parameter is required"), nil
		}
		pattern, err := req.RequireString("pattern")
		if err != nil {
			return mcp.NewToolResultError("pattern parameter is required"), nil
		}
		var matches []string
		err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil // skip unreadable subtrees
			}
			if strings.Contains(d.Name(), pattern) {
				matches = append(matches, path)
			}
			if len(matches) >= 200 {
				return filepath.SkipAll
			}
			return nil
		})
		if err != nil {
			return mcp.NewToolResultErrorFromErr("search failed", err), nil
		}
		if len(matches) == 0 {
			return mcp.NewToolResultText("No matches found."), nil
		}
		return mcp.NewToolResultText("Found " + fmt.Sprint(len(matches)) + " match(es):\n" + strings.Join(matches, "\n")), nil
	}))

	add(mcp.NewTool("get_file_info",
		mcp.WithDescription("Get detailed metadata (size, timestamps, permissions) for a file or directory"),
		pathFlag,
	), guard(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		p, err := req.RequireString("path")
		if err != nil {
			return mcp.NewToolResultError("path parameter is required"), nil
		}
		st, err := os.Stat(p)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("stat failed", err), nil
		}
		kind := "file"
		if st.IsDir() {
			kind = "directory"
		}
		return mcp.NewToolResultText(fmt.Sprintf(
			"path: %s\nsize: %d bytes\nkind: %s\npermissions: %s\nmodified: %s\n",
			p, st.Size(), kind, st.Mode().String(),
			st.ModTime().Format("2006-01-02 15:04:05"),
		)), nil
	}))

	add(mcp.NewTool("list_allowed_directories",
		mcp.WithDescription("List the directories this server is allowed to access"),
	), guard(func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("Allowed directories:\n" + strings.Join(allowedDirs, "\n")), nil
	}))

	if err := server.ServeStdio(s); err != nil {
		// Never write diagnostics to stdout — it is the JSON-RPC channel.
		fmt.Fprintf(os.Stderr, "mcp-filesystem: %v\n", err)
		os.Exit(1)
	}
}

// guard validates every path-ish argument against the allowed directory
// boundaries (symlink-resolved on both sides) before invoking the handler.
func guard(h server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		for _, key := range []string{"path", "source", "destination"} {
			if p := req.GetString(key, ""); p != "" {
				if err := checkBoundary(p); err != nil {
					return mcp.NewToolResultErrorf("access denied: %v", err), nil
				}
			}
		}
		for _, p := range req.GetStringSlice("paths", nil) {
			if p == "" {
				continue
			}
			if err := checkBoundary(p); err != nil {
				return mcp.NewToolResultErrorf("access denied: %v", err), nil
			}
		}
		return h(ctx, req)
	}
}

// checkBoundary resolves the path (including symlinks) and verifies it lives
// inside one of the allowed directories.
func checkBoundary(target string) error {
	abs, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("cannot resolve path: %w", err)
	}
	resolved := abs
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		resolved = r
	} else {
		// Target may not exist yet (write paths): validate its parent chain.
		resolved = resolveExistingParent(abs)
	}
	for _, dir := range allowedDirs {
		if resolved == dir || strings.HasPrefix(resolved, dir+string(filepath.Separator)) {
			return nil
		}
	}
	return fmt.Errorf("%s is outside the allowed directories", target)
}

func resolveExistingParent(abs string) string {
	dir := filepath.Dir(abs)
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return filepath.Join(r, filepath.Base(abs))
	}
	if dir == abs {
		return abs
	}
	return resolveExistingParent(dir)
}

func buildTree(root string, depth int) (*treeNode, error) {
	if depth > 12 {
		return nil, fmt.Errorf("tree too deep (max depth 12)")
	}
	st, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	node := &treeNode{Name: filepath.Base(root), IsDir: st.IsDir()}
	if !st.IsDir() {
		return node, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		if e.Name() == ".git" {
			continue
		}
		child, err := buildTree(filepath.Join(root, e.Name()), depth+1)
		if err != nil {
			continue
		}
		node.Children = append(node.Children, child)
	}
	return node, nil
}

type treeNode struct {
	Name     string      `json:"name"`
	IsDir    bool        `json:"isDir,omitempty"`
	Children []*treeNode `json:"children,omitempty"`
}

func requireTwo(req mcp.CallToolRequest, a, b string) (string, string, error) {
	va, err := req.RequireString(a)
	if err != nil {
		return "", "", fmt.Errorf("%s parameter is required", a)
	}
	vb, err := req.RequireString(b)
	if err != nil {
		return "", "", fmt.Errorf("%s parameter is required", b)
	}
	return va, vb, nil
}

func requireThree(req mcp.CallToolRequest, a, b, c string) (string, string, string, error) {
	va, vb, err := requireTwo(req, a, b)
	if err != nil {
		return "", "", "", err
	}
	vc, err := req.RequireString(c)
	if err != nil {
		return "", "", "", fmt.Errorf("%s parameter is required", c)
	}
	return va, vb, vc, nil
}
