package middleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
)

// Compression implementa compressão gzip de respostas,
// equivalente ao GZipMiddleware do FastAPI.
func Compression() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(w)
			defer gz.Close()

			next.ServeHTTP(&gzipResponseWriter{Writer: gz, ResponseWriter: w}, r)
		})
	}
}

// gzipResponseWriter envolve um http.ResponseWriter e comprime via gzip.
type gzipResponseWriter struct {
	io.Writer
	http.ResponseWriter
	headerSent bool
}

func (w *gzipResponseWriter) WriteHeader(code int) {
	if !w.headerSent {
		w.ResponseWriter.Header().Del("Content-Length")
		w.headerSent = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if !w.headerSent {
		w.WriteHeader(http.StatusOK)
	}
	return w.Writer.Write(b)
}
