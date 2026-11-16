package middleware

import (
	"net/http"
	"sync"
)

// LimitsConfig configura os middleware de limites.
type LimitsConfig struct {
	MaxBodyBytes     int64 // Tamanho máximo do body
	MaxHeaderBytes   int   // Tamanho máximo dos headers
	ConcurrencyLimit int   // Limite de requisições concorrentes
}

// BodyLimit middleware limita o tamanho do body da requisição.
func BodyLimit(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}

// ConcurrencyLimit middleware limita o número de requisições concorrentes (semáforo).
func ConcurrencyLimit(limit int) func(http.Handler) http.Handler {
	if limit <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}

	var (
		mu     sync.Mutex
		active int
	)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			if active >= limit {
				mu.Unlock()
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte(`{"error":"Server busy, too many concurrent requests"}`))
				return
			}
			active++
			mu.Unlock()

			defer func() {
				mu.Lock()
				active--
				mu.Unlock()
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// MaxConnections middleware limita o número total de conexões ativas.
func MaxConnections(max int) func(http.Handler) http.Handler {
	if max <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}

	var (
		mu     sync.Mutex
		active int
	)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			if active >= max {
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte(`{"error":"Server busy, too many connections"}`))
				mu.Unlock()
				return
			}
			active++
			mu.Unlock()

			defer func() {
				mu.Lock()
				active--
				mu.Unlock()
			}()

			next.ServeHTTP(w, r)
		})
	}
}
