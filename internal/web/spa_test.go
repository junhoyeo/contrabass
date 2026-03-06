package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSPAHandlerServesIndexAtRoot(t *testing.T) {
	h := SPAHandler(fstest.MapFS{
		"index.html": {Data: []byte("<html>dashboard</html>")},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	assert.Equal(t, "<html>dashboard</html>", rec.Body.String())
}

func TestSPAHandlerServesStaticAssetsWithContentType(t *testing.T) {
	h := SPAHandler(fstest.MapFS{
		"index.html":          {Data: []byte("<html>dashboard</html>")},
		"assets/index.js":     {Data: []byte("console.log('ok')")},
		"assets/index.css":    {Data: []byte("body{}")},
		"assets/index.js.map": {Data: []byte("{}")},
	})

	jsRec := httptest.NewRecorder()
	jsReq := httptest.NewRequest(http.MethodGet, "/assets/index.js", nil)
	h.ServeHTTP(jsRec, jsReq)
	assert.Equal(t, http.StatusOK, jsRec.Code)
	assert.Contains(t, jsRec.Header().Get("Content-Type"), "javascript")

	cssRec := httptest.NewRecorder()
	cssReq := httptest.NewRequest(http.MethodGet, "/assets/index.css", nil)
	h.ServeHTTP(cssRec, cssReq)
	assert.Equal(t, http.StatusOK, cssRec.Code)
	assert.Contains(t, cssRec.Header().Get("Content-Type"), "text/css")

	mapRec := httptest.NewRecorder()
	mapReq := httptest.NewRequest(http.MethodGet, "/assets/index.js.map", nil)
	h.ServeHTTP(mapRec, mapReq)
	assert.Equal(t, http.StatusNotFound, mapRec.Code)
}

func TestSPAHandlerFallbackServesIndexForUnknownPath(t *testing.T) {
	h := SPAHandler(fstest.MapFS{
		"index.html":       {Data: []byte("<html>fallback</html>")},
		"assets/index.js":  {Data: []byte("console.log('ok')")},
		"assets/index.css": {Data: []byte("body{}")},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	assert.Equal(t, "<html>fallback</html>", rec.Body.String())
}

func TestSPAHandlerDoesNotFallbackForAPIPath(t *testing.T) {
	h := SPAHandler(fstest.MapFS{
		"index.html": {Data: []byte("<html>fallback</html>")},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.NotContains(t, rec.Body.String(), "fallback")
}

func TestSPAHandlerSupportsEmbeddedRootPrefix(t *testing.T) {
	h := SPAHandler(fstest.MapFS{
		"packages/dashboard/dist/index.html":      {Data: []byte("<html>prefixed</html>")},
		"packages/dashboard/dist/assets/index.js": {Data: []byte("console.log('prefixed')")},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	body, err := io.ReadAll(rec.Result().Body)
	require.NoError(t, err)
	assert.Equal(t, "<html>prefixed</html>", string(body))
}
