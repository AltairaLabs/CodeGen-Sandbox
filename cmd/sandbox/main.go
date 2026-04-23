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
	addr := flag.String("addr", ":8080", "HTTP listen address")
	root := flag.String("workspace", "/workspace", "workspace root (absolute path)")
	devMode := flag.Bool("dev-mode", false, "trust-no-headers dev fallback: inject a placeholder identity when forwarded headers are absent")
	flag.Parse()

	// SIGINT for Ctrl-C; SIGTERM for docker stop and most orchestrators.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := Run(ctx, *addr, *root, *devMode); err != nil {
		log.Fatal(err)
	}
}
