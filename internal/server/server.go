package server

import (
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jigsmiths/dredge/web"
)

func New(opt Options) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.StripSlashes)

	r.Get("/health", healthHandler(opt.Version))

	r.Route("/api", (&api{opt}).routes)

	r.Handle("/*", spaHandler())

	return r
}

func spaHandler() http.Handler {
	sub, err := fs.Sub(web.FS, "dist")
	if err != nil {
		panic("failed to sub static files: " + err.Error())
	}
	fsHandler := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// fs.FS.Open requires paths without a leading slash.
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "."
		}

		if f, err := sub.Open(name); err == nil {
			if err := f.Close(); err != nil {
				slog.Warn("failed to close static file probe", "name", name, "err", err)
			}
			// Cache hashed assets indefinitely; everything else no-cache.
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			fsHandler.ServeHTTP(w, r)
			return
		}

		// SPA fallback: serve index.html for client-side routes.
		w.Header().Set("Cache-Control", "no-cache")
		r.URL.Path = "/"
		fsHandler.ServeHTTP(w, r)
	})
}
