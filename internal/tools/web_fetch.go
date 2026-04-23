package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/web"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	defaultWebFetchTimeoutSec = 30
	maxWebFetchTimeoutSec     = 120
	webFetchBodyCapBytes      = 1 * 1024 * 1024 // 1 MiB
	webFetchUserAgent         = "codegen-sandbox/0.1 (+WebFetch)"
)

// RegisterWebFetch registers the WebFetch tool.
func RegisterWebFetch(s Registrar, deps *Deps) {
	tool := mcp.NewTool("WebFetch",
		mcp.WithDescription("GET an http/https URL. URLs resolving to private/loopback/link-local/cloud-metadata addresses are rejected. Redirects are followed; each hop is filtered. Response body is capped at 1 MiB; the returned text starts with a Status/Content-Type header followed by a blank line and the body."),
		mcp.WithString("url", mcp.Required(), mcp.Description("Absolute http or https URL.")),
		mcp.WithNumber("timeout", mcp.Description(fmt.Sprintf("Timeout in seconds. Default %d, clamped to a maximum of %d.", defaultWebFetchTimeoutSec, maxWebFetchTimeoutSec))),
	)
	s.AddTool(tool, HandleWebFetch(deps))
}

// HandleWebFetch returns the WebFetch handler.
func HandleWebFetch(_ *Deps) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)
		rawurl, _ := args["url"].(string)
		if rawurl == "" {
			return ErrorResult("url is required"), nil
		}

		timeoutSec := defaultWebFetchTimeoutSec
		if v, ok := args["timeout"].(float64); ok && int(v) > 0 {
			timeoutSec = int(v)
			if timeoutSec > maxWebFetchTimeoutSec {
				timeoutSec = maxWebFetchTimeoutSec
			}
		}

		fetchCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		defer cancel()

		if err := web.CheckURL(fetchCtx, rawurl); err != nil {
			return ErrorResult("url rejected: %v", err), nil
		}

		client := &http.Client{
			Timeout: time.Duration(timeoutSec) * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return errors.New("stopped after 10 redirects")
				}
				// Re-filter each hop: an allowed URL can redirect to a
				// blocked one.
				return web.CheckURL(req.Context(), req.URL.String())
			},
		}

		httpReq, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, rawurl, nil)
		if err != nil {
			return ErrorResult("request: %v", err), nil
		}
		httpReq.Header.Set("User-Agent", webFetchUserAgent)

		resp, err := client.Do(httpReq)
		if err != nil {
			return ErrorResult("fetch: %v", err), nil
		}
		defer func() { _ = resp.Body.Close() }()

		body, truncated, err := readCapped(resp.Body, webFetchBodyCapBytes)
		if err != nil {
			return ErrorResult("read body: %v", err), nil
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "Status: %d\n", resp.StatusCode)
		if ct := resp.Header.Get("Content-Type"); ct != "" {
			fmt.Fprintf(&sb, "Content-Type: %s\n", ct)
		}
		if truncated {
			fmt.Fprintf(&sb, "Truncated: true (first %d bytes)\n", webFetchBodyCapBytes)
		}
		sb.WriteString("\n")
		sb.Write(body)
		return TextResult(sb.String()), nil
	}
}

// readCapped reads at most limit bytes from r, returning truncated=true if
// more data was available.
func readCapped(r io.Reader, limit int) (body []byte, truncated bool, err error) {
	body, err = io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return nil, false, err
	}
	if len(body) > limit {
		return body[:limit], true, nil
	}
	return body, false, nil
}
