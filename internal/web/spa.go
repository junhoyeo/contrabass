package web

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

func SPAHandler(fsys fs.FS) http.Handler {
	if fsys == nil {
		return http.NotFoundHandler()
	}

	if subFS, err := fs.Sub(fsys, "packages/dashboard/dist"); err == nil {
		if _, statErr := fs.Stat(subFS, "index.html"); statErr == nil {
			fsys = subFS
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}

		requestPath := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if requestPath == "" || r.URL.Path == "/" {
			requestPath = "index.html"
		}

		if strings.HasSuffix(requestPath, ".map") {
			http.NotFound(w, r)
			return
		}

		if served, err := serveSPAFile(w, r, fsys, requestPath); err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		} else if served {
			return
		}

		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api" {
			http.NotFound(w, r)
			return
		}

		if served, err := serveSPAFile(w, r, fsys, "index.html"); err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		} else if !served {
			http.NotFound(w, r)
		}
	})
}

func serveSPAFile(w http.ResponseWriter, r *http.Request, fsys fs.FS, name string) (bool, error) {
	f, err := fsys.Open(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return false, err
	}
	if stat.IsDir() {
		return false, nil
	}

	data, err := io.ReadAll(f)
	if err != nil {
		return false, err
	}

	if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}

	http.ServeContent(w, r, name, stat.ModTime(), bytes.NewReader(data))
	return true, nil
}
