package api

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
)

//go:embed openapi.yaml swagger-ui
var files embed.FS

func Register(router chi.Router) error {
	schema, err := files.ReadFile("openapi.yaml")
	if err != nil {
		return err
	}
	assets, err := fs.Sub(files, "swagger-ui")
	if err != nil {
		return err
	}
	ui := http.StripPrefix("/docs/", http.FileServer(http.FS(assets)))
	router.Get("/docs", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/docs/", http.StatusTemporaryRedirect)
	})
	router.Get("/docs/*", ui.ServeHTTP)
	router.Get("/api/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		_, _ = w.Write(schema)
	})
	return nil
}
