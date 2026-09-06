package api

import (
	"embed"
	"io/fs"
	"net/http"
)

// staticFS is the embedded web UI (vanilla HTML/JS/CSS, zero npm deps —
// DECISIONS D5). It ships inside the xcut binary: `xcut serve` is the whole
// product.
//
//go:embed all:static
var staticFS embed.FS

// registerStatic mounts the embedded UI at "/". API routes take precedence
// because they are registered with more specific patterns.
func registerStatic(mux *http.ServeMux) {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic("api: embedded static FS broken: " + err.Error())
	}
	mux.Handle("GET /", http.FileServer(http.FS(sub)))
}
