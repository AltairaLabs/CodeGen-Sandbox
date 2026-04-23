package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/altairalabs/codegen-sandbox/internal/workspace"
)

// treeEntry is one node in the workspace tree listing.
type treeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"` // "file" or "dir"
	Size int64  `json:"size,omitempty"`
}

// treeResponse is the JSON body returned by GET /api/tree.
type treeResponse struct {
	Root    string      `json:"root"`
	Entries []treeEntry `json:"entries"`
}

// treeHandler returns files + parent dirs for the workspace, honouring
// .gitignore via `rg --files`.
func treeHandler(ws *workspace.Workspace) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		files, err := ripgrepFiles(r.Context(), ws.Root())
		if err != nil {
			http.Error(w, "list files: "+err.Error(), http.StatusInternalServerError)
			return
		}

		entries := buildTree(ws.Root(), files)
		sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(treeResponse{Root: ws.Root(), Entries: entries})
	})
}

// buildTree turns a list of workspace-relative file paths into a combined
// file+dir entry list. Directory sizes are omitted.
func buildTree(root string, files []string) []treeEntry {
	out := make([]treeEntry, 0, len(files))
	dirs := make(map[string]struct{})

	for _, f := range files {
		abs := filepath.Join(root, f)
		var size int64
		if info, err := os.Stat(abs); err == nil {
			size = info.Size()
		}
		out = append(out, treeEntry{Path: f, Type: "file", Size: size})

		// Collect ancestor directories.
		dir := filepath.Dir(f)
		for dir != "." && dir != "/" && dir != "" {
			dirs[dir] = struct{}{}
			dir = filepath.Dir(dir)
		}
	}

	for d := range dirs {
		out = append(out, treeEntry{Path: d, Type: "dir"})
	}
	return out
}

// ripgrepFiles shells to `rg --files`, honouring .gitignore and the sandbox's
// baseline excludes (.git, node_modules).
func ripgrepFiles(ctx context.Context, root string) ([]string, error) {
	path, err := exec.LookPath("rg")
	if err != nil {
		return nil, fmt.Errorf("ripgrep (rg) not found on PATH")
	}
	args := []string{
		"--files",
		"--hidden",
		"--no-require-git",
		"--color=never",
		"--glob=!.git",
		"--glob=!node_modules",
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			// rg exit 1 = no matches.
			return nil, nil
		}
		return nil, fmt.Errorf("rg: %w: %s", err, stderr.String())
	}
	raw := strings.TrimRight(stdout.String(), "\n")
	if raw == "" {
		return nil, nil
	}
	return strings.Split(raw, "\n"), nil
}
