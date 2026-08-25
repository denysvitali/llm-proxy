package server

import (
	"embed"
	"net/http"
	"path"
	"path/filepath"
	"strings"
)

// webDist holds the compiled dashboard SPA (source in web/ at the repo root;
// build artifacts land here via scripts/build-web.sh or the Dockerfile). The
// committed placeholder keeps plain `go build` working without Node.
//
//go:embed all:web/webdist
var webDist embed.FS

const webDistRoot = "web/webdist"

// handleSPA serves the embedded single-page app. Unknown paths fall back to
// index.html so client-side routing works; API routes registered on the mux
// always win over the wildcard that lands here.
func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if b, ok := readWebAsset(name); ok {
		writeWebAsset(w, name, b)
		return
	}
	index, ok := readWebAsset("index.html")
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeWebAsset(w, "index.html", index)
}

func readWebAsset(name string) ([]byte, bool) {
	if name == "" || strings.HasSuffix(name, "/") {
		return nil, false
	}
	b, err := webDist.ReadFile(webDistRoot + "/" + name)
	return b, err == nil
}

func writeWebAsset(w http.ResponseWriter, name string, b []byte) {
	w.Header().Set("Content-Type", contentTypeFor(name))
	_, _ = w.Write(b)
}

func contentTypeFor(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".ico":
		return "image/x-icon"
	case ".json", ".map":
		return "application/json"
	case ".woff2":
		return "font/woff2"
	default:
		return "application/octet-stream"
	}
}
