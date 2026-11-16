package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
)

// Recovery recupera panics ocorridos durante o tratamento de uma requisição
// e retorna uma resposta HTTP 500 Interno, sem expor stack traces ao cliente.
// O stack trace é enviado ao logger para diagnóstico interno.
func Recovery(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					// Logar o stack trace internamente para diagnóstico
					logger.Error("panic recuperado em handler HTTP",
						"panic", fmt.Sprintf("%v", rec),
						"method", r.Method,
						"path", r.URL.Path,
					)

					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					// Nunca expor stack ou details internos ao cliente
					fmt.Fprintf(w, `{"error":"erro interno do servidor"}`)
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}
