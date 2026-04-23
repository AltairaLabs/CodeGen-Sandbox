// Package main is the entry point for the codegen-sandbox MCP server.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address for the MCP server")
	apiAddr := flag.String("api-addr", "", "HTTP listen address for the human-facing API (empty = disabled)")
	enableAPI := flag.Bool("enable-api", false, "mount /api/tree, /api/file, /api/events on -api-addr")
	enableExec := flag.Bool("enable-exec", false, "mount /api/exec (WebSocket PTY) on -api-addr")
	root := flag.String("workspace", "/workspace", "workspace root (absolute path)")
	devMode := flag.Bool("dev-mode", false, "trust-no-headers dev fallback: inject a placeholder identity when forwarded headers are absent")
	flag.Parse()

	// SIGINT for Ctrl-C; SIGTERM for docker stop and most orchestrators.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := Run(ctx, *addr, *apiAddr, *root, *devMode, *enableAPI, *enableExec); err != nil {
		log.Fatal(err)
	}
}
