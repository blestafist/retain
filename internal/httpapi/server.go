package httpapi

import (
	"context"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

const healthCheckTimeout = 2 * time.Second

type databasePinger interface {
	PingContext(context.Context) error
}

// NewHandler wires the health endpoint and the embedded frontend. Frontend
// build output is optional so the server package also works in a fresh checkout.
func NewHandler(database databasePinger, frontend fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthHandler(database))
	mux.Handle("GET /", frontendHandler(frontend))
	return mux
}

func healthHandler(database databasePinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), healthCheckTimeout)
		defer cancel()

		if database == nil || database.PingContext(ctx) != nil {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	}
}

func frontendHandler(frontend fs.FS) http.Handler {
	generated, err := fs.Sub(frontend, "generated")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		})
	}

	files := http.FileServer(http.FS(generated))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if name == ".gitkeep" || !fs.ValidPath(name) {
			http.NotFound(w, r)
			return
		}

		info, err := fs.Stat(generated, name)
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Cache-Control", "no-cache")
		if name == "index.html" || strings.HasSuffix(name, ".html") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		} else if isHashedAsset(name) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}

		fileRequest := r.Clone(r.Context())
		if name == "index.html" {
			fileRequest.URL.Path = "/"
		} else {
			fileRequest.URL.Path = "/" + path.Clean(name)
		}
		fileRequest.URL.RawPath = ""
		files.ServeHTTP(w, fileRequest)
	})
}

func isHashedAsset(name string) bool {
	filename := path.Base(name)
	extension := path.Ext(filename)
	if extension == "" {
		return false
	}
	stem := strings.TrimSuffix(filename, extension)
	separator := strings.LastIndexByte(stem, '-')
	if separator < 0 {
		return false
	}
	hash := stem[separator+1:]
	if len(hash) < 8 {
		return false
	}
	for _, char := range hash {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') &&
			(char < '0' || char > '9') && char != '_' && char != '-' {
			return false
		}
	}
	return true
}
