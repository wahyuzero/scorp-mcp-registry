// mcp-fetch is a pure-Go MCP server for web content fetching and markdown
// extraction. It is a native Go port of the reference `fetch` MCP server
// (modelcontextprotocol/servers, TypeScript), rebuilt on the official
// mark3labs/mcp-go SDK with a ~5MB RAM footprint and <20ms cold start.
//
// Tools:
//   - fetch: retrieve a web URL and return its content as markdown
//     (or raw HTML with raw=true), with pagination via start_index.
//
// Copyright (c) 2026 Wahyu — MIT License.
// Upstream: https://github.com/modelcontextprotocol/servers/tree/main/src/fetch (MIT).
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	md "github.com/JohannesKaufmann/html-to-markdown"
	readability "github.com/go-shiori/go-readability"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	version        = "1.1.0"
	userAgent      = "scorp-mcp-fetch/" + version
	maxBodyBytes   = 2 << 20 // 2MB hard ceiling on response bodies
	defaultMaxLen  = 20000
	httpTimeout    = 20 * time.Second
	chunkReadTime  = 30 * time.Second
)

var httpClient = &http.Client{Timeout: httpTimeout}

func main() {
	s := server.NewMCPServer(
		"mcp-fetch",
		version,
		server.WithToolCapabilities(false),
		server.WithRecovery(),
	)

	fetchTool := mcp.NewTool("fetch",
		mcp.WithDescription(
			"Fetches a URL from the internet and extracts its content as markdown. "+
				"Reads the main article content using a Firefox Reader-style extraction engine. "+
				"Returns the content truncated to max_length characters; paginate with start_index.",
		),
		mcp.WithString("url",
			mcp.Required(),
			mcp.Description("URL to fetch"),
		),
		mcp.WithNumber("max_length",
			mcp.Description("Maximum number of characters to return (default 20000)"),
		),
		mcp.WithNumber("start_index",
			mcp.Description("Start position in the extracted content (for pagination)"),
		),
		mcp.WithBoolean("raw",
			mcp.Description("Return raw HTML instead of extracted markdown"),
		),
	)

	s.AddTool(fetchTool, handleFetch)

	if err := server.ServeStdio(s); err != nil {
		// Never write diagnostics to stdout — it is the JSON-RPC channel.
		fmt.Fprintf(os.Stderr, "mcp-fetch: %v\n", err)
		os.Exit(1)
	}
}

// handleFetch implements the fetch tool contract.
func handleFetch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rawURL, err := req.RequireString("url")
	if err != nil {
		return mcp.NewToolResultError("url parameter is required"), nil
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return mcp.NewToolResultErrorf("invalid URL: %v", err), nil
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return mcp.NewToolResultError("only http and https schemes are supported"), nil
	}

	maxLength := int(req.GetFloat("max_length", defaultMaxLen))
	if maxLength <= 0 {
		maxLength = defaultMaxLen
	}
	startIndex := int(req.GetFloat("start_index", 0))
	if startIndex < 0 {
		startIndex = 0
	}
	wantRaw := req.GetBool("raw", false)

	body, contentType, err := httpGet(ctx, rawURL)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("fetch failed", err), nil
	}

	var content string
	switch {
	case wantRaw:
		content = body
	case isHTML(contentType):
		content, err = extractMarkdown(rawURL, body)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("extraction failed", err), nil
		}
	default:
		content = body
	}

	prefix := ""
	if startIndex > 0 {
		if startIndex >= len(content) {
			return mcp.NewToolResultErrorf("start_index %d is beyond content length %d", startIndex, len(content)), nil
		}
		prefix = fmt.Sprintf("[Content retrieved from %s, showing characters %d-%d]\n\n", rawURL, startIndex, min(startIndex+maxLength, len(content)))
		content = content[startIndex:]
	}

	if len(content) > maxLength {
		content = content[:maxLength] + fmt.Sprintf("\n\n<error>Content truncated. Call the fetch tool with start_index=%d to continue.</error>", startIndex+maxLength)
	}

	return mcp.NewToolResultText(prefix + content), nil
}

// httpGet performs a bounded GET request and returns the decoded body.
func httpGet(ctx context.Context, target string) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, chunkReadTime)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9,id;q=0.8")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return "", "", fmt.Errorf("HTTP status %s", resp.Status)
	}

	limited := io.LimitReader(resp.Body, maxBodyBytes)
	buf, err := io.ReadAll(limited)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", "", err
	}
	return string(buf), resp.Header.Get("Content-Type"), nil
}

func isHTML(contentType string) bool {
	return strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/xhtml")
}

// extractMarkdown runs readability extraction then converts the purified
// HTML to markdown, falling back to plain text when conversion yields nothing.
func extractMarkdown(target, html string) (string, error) {
	parsed, _ := url.Parse(target)
	article, err := readability.FromReader(strings.NewReader(html), parsed)
	if err != nil {
		return "", fmt.Errorf("readability extraction: %w", err)
	}

	converter := md.NewConverter("", true, nil)
	markdown, err := converter.ConvertString(article.Content)
	if err != nil || strings.TrimSpace(markdown) == "" {
		markdown = article.TextContent
	}

	var sb strings.Builder
	if article.Title != "" {
		sb.WriteString("# " + article.Title + "\n\n")
	}
	if article.Byline != "" {
		sb.WriteString("*By " + article.Byline + "*\n\n")
	}
	sb.WriteString(markdown)
	return sb.String(), nil
}
