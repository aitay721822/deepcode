// Package webui serves the React build embedded into the binary.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// dist is copied from web/dist by scripts/build.sh; "all:" keeps the
// .gitkeep placeholder so the package compiles before the first UI build.
//
//go:embed all:dist
var assets embed.FS

func Handler() (http.Handler, error) {
	dist, err := fs.Sub(assets, "dist")
	if err != nil {
		return nil, err
	}
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "The deepcode UI is not built into this binary: run scripts/build.sh, or use the Vite dev server in web/.", http.StatusNotFound)
		}), nil
	}
	return spa(dist), nil
}

// spa serves the files in dist, and index.html for every other page a browser
// loads, so client-side routes work on reload, including repositories named
// like files (/vercel/next.js).
func spa(dist fs.FS) http.Handler {
	static := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name != "" {
			if info, err := fs.Stat(dist, name); err == nil && !info.IsDir() {
				if strings.HasPrefix(name, "assets/") { // content-hashed by Vite
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				static.ServeHTTP(w, r)
				return
			}
			// A missing build asset, or a request that is not a page load (an
			// image, a script), gets a plain 404 rather than the app.
			if strings.HasPrefix(name, "assets/") || !strings.Contains(r.Header.Get("Accept"), "text/html") {
				http.NotFound(w, r)
				return
			}
		}
		// SPA route: serve index.html. FileServer redirects "/index.html" to
		// "/", so ask for the directory itself.
		w.Header().Set("Cache-Control", "no-cache")
		fallback := r.Clone(r.Context())
		fallback.URL.Path = "/"
		static.ServeHTTP(w, fallback)
	})
}
