package httpapi

import (
	"net/http"
	"strconv"

	"github.com/pestit/retain/internal/importer"
)

const retainV1SchemaContentType = "application/schema+json; charset=utf-8"
const retainV1SchemaCacheControl = "public, max-age=3600"

func retainV1SchemaHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", retainV1SchemaContentType)
	w.Header().Set("Cache-Control", retainV1SchemaCacheControl)
	w.Header().Set("Content-Length", strconv.Itoa(len(importer.RetainV1Schema)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(importer.RetainV1Schema)
}
