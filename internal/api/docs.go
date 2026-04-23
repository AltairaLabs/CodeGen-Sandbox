package api

import (
	_ "embed"
	"net/http"
)

//go:embed openapi.yaml
var openAPISpec []byte

// OpenAPISpec returns the embedded OpenAPI specification bytes.
func OpenAPISpec() []byte { return openAPISpec }

// openAPIHandler serves the embedded OpenAPI YAML.
func openAPIHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(openAPISpec)
}

// docsHandler renders a minimal HTML page that loads Scalar's API
// reference component from a CDN and points it at /api/openapi.yaml.
// The page has no external branding beyond the title; Scalar reads the
// `info.title` / `info.description` fields from the spec.
func docsHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(docsHTML))
}

const docsHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Codegen Sandbox API</title>
</head>
<body>
  <script id="api-reference" data-url="/api/openapi.yaml"></script>
  <script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
</body>
</html>`
