// Package main is the entry point for the codegen-sandbox MCP server.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address for the MCP server")
	apiAddr := flag.String("api-addr", "", "HTTP listen address for the human-facing API (empty = disabled)")
	metricsAddr := flag.String("metrics-addr", "", "HTTP listen address for the Prometheus /metrics endpoint (empty = disabled)")
	metricsToolRepetitionWindow := flag.Duration("metrics-tool-repetition-window", 10*time.Minute, "Time window over which agent-health repetition counts (tool,args) repeats")
	metricsToolRepetitionThreshold := flag.Int("metrics-tool-repetition-threshold", 3, "Minimum (tool,args) repeats within the window before agent-health emits a repetition burst")
	metricsErrorRateWindow := flag.Int("metrics-error-rate-window", 100, "Size of the rolling tool-outcome buffer feeding the agent-health tool_error_rate gauge")
	otlpEndpoint := flag.String("otlp-endpoint", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), "OTLP-HTTP exporter URL for OpenTelemetry tracing (e.g. http://otel-collector:4318). Empty disables tracing; defaults to $OTEL_EXPORTER_OTLP_ENDPOINT.")
	enableAPI := flag.Bool("enable-api", false, "mount /api/tree, /api/file, /api/events on -api-addr")
	enableExec := flag.Bool("enable-exec", false, "mount /api/exec (WebSocket PTY) on -api-addr")
	enablePortForward := flag.Bool("enable-port-forward", false, "mount /api/port-forward (WebSocket TCP tunnel to loopback) on -api-addr")
	enableSSH := flag.Bool("enable-ssh", false, "start embedded SSH server on 127.0.0.1 and mount /api/ssh-authorized-keys + /api/ssh-port on -api-addr")
	root := flag.String("workspace", "/workspace", "workspace root (absolute path)")
	devMode := flag.Bool("dev-mode", false, "trust-no-headers dev fallback: inject a placeholder identity when forwarded headers are absent")
	secretsDir := flag.String("secrets-dir", "", "directory of one-file-per-secret mounts (e.g. k8s Secret volume). Empty disables the file source; CODEGEN_SANDBOX_SECRET_* env vars still work.")
	flag.Parse()

	// SIGINT for Ctrl-C; SIGTERM for docker stop and most orchestrators.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := Run(ctx, Config{
		Addr:                        *addr,
		APIAddr:                     *apiAddr,
		MetricsAddr:                 *metricsAddr,
		MetricsToolRepetitionWindow: *metricsToolRepetitionWindow,
		MetricsToolRepetitionThresh: *metricsToolRepetitionThreshold,
		MetricsErrorRateWindow:      *metricsErrorRateWindow,
		WorkspaceRoot:               *root,
		DevMode:                     *devMode,
		EnableAPI:                   *enableAPI,
		EnableExec:                  *enableExec,
		EnablePortForward:           *enablePortForward,
		EnableSSH:                   *enableSSH,
		SecretsDir:                  *secretsDir,
		OTLPEndpoint:                *otlpEndpoint,
	}); err != nil {
		log.Fatal(err)
	}
}
