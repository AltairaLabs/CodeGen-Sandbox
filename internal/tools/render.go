package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
	"github.com/mark3labs/mcp-go/mcp"
)

// ErrMermaidCLIMissing / ErrGraphvizDotMissing signal that the render
// binary is not on PATH. Surfaced as actionable errors pointing operators
// to the codegen-sandbox-tools-render feature layer.
var (
	ErrMermaidCLIMissing = errors.New(
		"mermaid-cli (mmdc) not found on PATH — compose the codegen-sandbox-tools-render layer or install mmdc on the sandbox image",
	)
	ErrGraphvizDotMissing = errors.New(
		"graphviz (dot) not found on PATH — compose the codegen-sandbox-tools-render layer or install graphviz on the sandbox image",
	)
)

const (
	defaultRenderTimeoutSec = 60
	maxRenderTimeoutSec     = 300
	// maxRenderSourceBytes caps how big an agent-supplied diagram source
	// can be. Bigger than this is almost always a bug or a prompt-injection
	// blob, not a legitimate diagram.
	maxRenderSourceBytes = 1 * 1024 * 1024
	// maxRenderOutputBytes caps the rendered artifact size. Anything larger
	// is deleted and the call returns an error — prevents a runaway diagram
	// from filling the workspace volume.
	maxRenderOutputBytes = 10 * 1024 * 1024
)

// supportedRenderFormats maps output extensions to the format name both
// mmdc and dot understand. Keeping the set tight (svg / png / pdf) avoids
// dot's long tail of exotic formats (`-Tplain` leaks filesystem names via
// error messages, `-Tcanon` emits source-like DOT that muddles the
// agent → artifact contract) and keeps the tool surface obvious.
var supportedRenderFormats = map[string]string{
	".svg": "svg",
	".png": "png",
	".pdf": "pdf",
}

// RegisterRender registers render_mermaid + render_dot on the MCP server.
func RegisterRender(s ToolAdder, deps *Deps) {
	registerRenderMermaid(s, deps)
	registerRenderDot(s, deps)
}

func registerRenderMermaid(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("render_mermaid",
		mcp.WithDescription(
			"Render a Mermaid diagram source to SVG, PNG, or PDF via mmdc (mermaid-cli). "+
				"Output format is inferred from the output_path extension (.svg / .png / .pdf). "+
				"Runtime requires mmdc on PATH — the codegen-sandbox-tools-render feature "+
				"layer provides it. Source is capped at 1 MiB, output at 10 MiB. Output is "+
				"written to the workspace and left available for subsequent Read / download.",
		),
		mcp.WithString("source", mcp.Required(),
			mcp.Description("Mermaid diagram source (graph / sequenceDiagram / flowchart / etc.).")),
		mcp.WithString("output_path", mcp.Required(),
			mcp.Description("Workspace-relative output path. Extension selects format (.svg, .png, .pdf).")),
		mcp.WithNumber("timeout",
			mcp.Description(fmt.Sprintf("Timeout in seconds. Default %d, clamped to a maximum of %d.",
				defaultRenderTimeoutSec, maxRenderTimeoutSec))),
	)
	s.AddTool(tool, handleRender(deps, renderKindMermaid))
}

func registerRenderDot(s ToolAdder, deps *Deps) {
	tool := mcp.NewTool("render_dot",
		mcp.WithDescription(
			"Render a Graphviz DOT source to SVG, PNG, or PDF via dot. "+
				"Output format is inferred from the output_path extension (.svg / .png / .pdf). "+
				"Runtime requires dot on PATH — the codegen-sandbox-tools-render feature "+
				"layer provides it. Source is capped at 1 MiB, output at 10 MiB. Output is "+
				"written to the workspace and left available for subsequent Read / download.",
		),
		mcp.WithString("source", mcp.Required(),
			mcp.Description("Graphviz DOT source (digraph / graph ... { ... }).")),
		mcp.WithString("output_path", mcp.Required(),
			mcp.Description("Workspace-relative output path. Extension selects format (.svg, .png, .pdf).")),
		mcp.WithNumber("timeout",
			mcp.Description(fmt.Sprintf("Timeout in seconds. Default %d, clamped to a maximum of %d.",
				defaultRenderTimeoutSec, maxRenderTimeoutSec))),
	)
	s.AddTool(tool, handleRender(deps, renderKindDot))
}

type renderKind int

const (
	renderKindMermaid renderKind = iota
	renderKindDot
)

func (k renderKind) toolName() string {
	if k == renderKindMermaid {
		return "render_mermaid"
	}
	return "render_dot"
}

// handleRender returns an MCP handler for render_mermaid (kind == renderKindMermaid)
// or render_dot (kind == renderKindDot). The two share parse / resolve / size-cap
// logic; only the subprocess invocation differs.
func handleRender(deps *Deps, kind renderKind) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)

		source, outputPath, timeoutSec, errRes := parseRenderArgs(args)
		if errRes != nil {
			return errRes, nil
		}
		format, errRes := inferRenderFormat(outputPath)
		if errRes != nil {
			return errRes, nil
		}
		absOut, errRes := resolveRenderOutput(deps, outputPath)
		if errRes != nil {
			return errRes, nil
		}

		execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		defer cancel()

		runErr := runRenderSubprocess(execCtx, deps.Workspace.Root(), kind, source, format, absOut)
		if runErr != nil {
			_ = os.Remove(absOut)
			if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
				return ErrorResult("%s: timed out after %ds", kind.toolName(), timeoutSec), nil
			}
			return ErrorResult("%s: %v", kind.toolName(), runErr), nil
		}

		info, err := os.Stat(absOut)
		if err != nil {
			return ErrorResult("%s: stat output: %v", kind.toolName(), err), nil
		}
		if info.Size() > maxRenderOutputBytes {
			_ = os.Remove(absOut)
			return ErrorResult(
				"%s: output %d bytes exceeds cap %d; reduce diagram complexity or use a lighter format",
				kind.toolName(), info.Size(), maxRenderOutputBytes,
			), nil
		}

		// Render writes via a subprocess, not the Write tool's atomic path.
		// Mark it Read so the agent can immediately Read or edit the artifact
		// without tripping the read-before-overwrite guard.
		deps.Tracker.MarkRead(absOut)

		return TextResult(fmt.Sprintf("wrote %d bytes to %s (%s)", info.Size(), outputPath, format)), nil
	}
}

func parseRenderArgs(args map[string]any) (source, outputPath string, timeoutSec int, errRes *mcp.CallToolResult) {
	source, _ = args["source"].(string)
	if source == "" {
		return "", "", 0, ErrorResult("source is required")
	}
	if len(source) > maxRenderSourceBytes {
		return "", "", 0, ErrorResult("source %d bytes exceeds cap %d", len(source), maxRenderSourceBytes)
	}
	outputPath, _ = args["output_path"].(string)
	if outputPath == "" {
		return "", "", 0, ErrorResult("output_path is required")
	}
	timeoutSec = defaultRenderTimeoutSec
	if v, ok := args["timeout"].(float64); ok && int(v) > 0 {
		timeoutSec = int(v)
		if timeoutSec > maxRenderTimeoutSec {
			timeoutSec = maxRenderTimeoutSec
		}
	}
	return source, outputPath, timeoutSec, nil
}

func inferRenderFormat(outputPath string) (string, *mcp.CallToolResult) {
	ext := strings.ToLower(filepath.Ext(outputPath))
	format, ok := supportedRenderFormats[ext]
	if !ok {
		return "", ErrorResult("unsupported output extension %q; supported: .svg, .png, .pdf", ext)
	}
	return format, nil
}

func resolveRenderOutput(deps *Deps, outputPath string) (string, *mcp.CallToolResult) {
	abs, err := deps.Workspace.Resolve(outputPath)
	if err != nil {
		if errors.Is(err, workspace.ErrOutsideWorkspace) {
			deps.Metrics.PathViolation()
		}
		return "", ErrorResult("resolve path: %v", err)
	}
	if info, statErr := os.Stat(abs); statErr == nil && info.IsDir() {
		return "", ErrorResult("output_path is a directory: %s", outputPath)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", ErrorResult("mkdir: %v", err)
	}
	return abs, nil
}

func runRenderSubprocess(ctx context.Context, cwd string, kind renderKind, source, format, absOut string) error {
	switch kind {
	case renderKindMermaid:
		return runMermaid(ctx, cwd, source, absOut)
	case renderKindDot:
		return runDot(ctx, cwd, source, format, absOut)
	default:
		return fmt.Errorf("unknown render kind %d", kind)
	}
}

// runMermaid writes source to a temp file next to the output and invokes
// mmdc. mmdc only accepts an input filename (no stdin mode), so a temp
// file is unavoidable. It's placed next to the output so any agent-side
// .mermaidrc lookup rooted at the source directory finds the same config
// the agent set up.
func runMermaid(ctx context.Context, cwd, source, absOut string) error {
	path, err := exec.LookPath("mmdc")
	if err != nil {
		return ErrMermaidCLIMissing
	}
	tmp, err := os.CreateTemp(filepath.Dir(absOut), ".render-*.mmd")
	if err != nil {
		return fmt.Errorf("tempfile: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.WriteString(source); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write tempfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tempfile: %w", err)
	}

	cmd := exec.CommandContext(ctx, path, "-i", tmpPath, "-o", absOut)
	cmd.Dir = cwd
	cmd.Stdin = nil
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mmdc: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// runDot pipes source into dot via stdin — dot accepts source on stdin
// when no input file is given, so we avoid writing a temp file for the
// dot path. `-T<format>` selects the output format explicitly; `-o` sets
// the output file.
func runDot(ctx context.Context, cwd, source, format, absOut string) error {
	path, err := exec.LookPath("dot")
	if err != nil {
		return ErrGraphvizDotMissing
	}
	cmd := exec.CommandContext(ctx, path, "-T"+format, "-o", absOut)
	cmd.Dir = cwd
	cmd.Stdin = strings.NewReader(source)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("dot: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// LookupMermaidCLI returns nil if mmdc is on PATH, or ErrMermaidCLIMissing
// otherwise. Intended for use by integration tests that want to skip when
// the render layer isn't available locally.
func LookupMermaidCLI() error {
	if _, err := exec.LookPath("mmdc"); err != nil {
		return ErrMermaidCLIMissing
	}
	return nil
}

// LookupGraphvizDot returns nil if dot is on PATH, or ErrGraphvizDotMissing
// otherwise.
func LookupGraphvizDot() error {
	if _, err := exec.LookPath("dot"); err != nil {
		return ErrGraphvizDotMissing
	}
	return nil
}
