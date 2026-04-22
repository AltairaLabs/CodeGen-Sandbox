// Package main is the entry point for the codegen-sandbox MCP server.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/altairalabs/codegen-sandbox/internal/server"
	"github.com/altairalabs/codegen-sandbox/internal/workspace"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	root := flag.String("workspace", "/workspace", "workspace root (absolute path)")
	flag.Parse()

	ws, err := workspace.New(*root)
	if err != nil {
		log.Fatalf("workspace: %v", err)
	}

	srv, err := server.New(ws)
	if err != nil {
		log.Fatalf("server: %v", err)
	}

	log.Printf("codegen-sandbox listening on %s (workspace=%s)", *addr, ws.Root())
	if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
