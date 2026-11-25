// Package web serves the built single-page app from inside the binary.
//
// Embedding it is the point of the rewrite: deployment is one file, and the
// panel cannot come up with a missing asset directory.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the SPA. Real files are served as they are; every other
// path gets index.html so the client-side router can pick it up, which is
// what a browser reload on /accounts needs.
func Handler() http.Handler {
	content, err := fs.Sub(dist, "dist")
	if err != nil {
		// Only possible if the embed pattern stopped matching, which the
		// build would have caught.
		panic("the embedded panel assets are missing: " + err.Error())
	}

	files := http.FileServer(http.FS(content))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

			return
		}

		urlPath := path.Clean(r.URL.Path)

		// An unknown API path is a mistake in the client, not a page.
		if strings.HasPrefix(urlPath, "/api/") {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"No such endpoint."}}`))

			return
		}

		if urlPath != "/" {
			if file, err := content.Open(strings.TrimPrefix(urlPath, "/")); err == nil {
				_ = file.Close()

				// Vite writes a content hash into every asset name, so an
				// asset can be cached for as long as a browser likes. The
				// HTML must not be, or a new release would never be seen.
				if strings.HasPrefix(urlPath, "/assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}

				files.ServeHTTP(w, r)

				return
			}
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")

		index, err := fs.ReadFile(content, "index.html")
		if err != nil {
			http.Error(w, "the panel's interface is missing from this build", http.StatusInternalServerError)

			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(index)
	})
}
