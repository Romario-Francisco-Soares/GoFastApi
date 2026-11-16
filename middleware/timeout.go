package middleware

import (
	"context"
	"net/http"
	"time"
)

// Timeout limita o tempo de execução de um handler.
// Se o handler não finalizar dentro do tempo limite, retorna 503 Service Unavailable.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()

			done := make(chan struct{})

			go func() {
				defer close(done)
				next.ServeHTTP(&timeoutResponseWriter{ResponseWriter: w}, r.WithContext(ctx))
			}()

			select {
			case <-done:
				return
			case <-ctx.Done():
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				// Não expor details internos
				_, _ = w.Write([]byte(`{"error":"request timeout"}`))
			}
		})
	}
}

// timeoutResponseWriter captura se o cabeçalho já foi enviado para evitar
// escrever múltiplos cabeçalhos quando o timeout ocorre durante a resposta.
type timeoutResponseWriter struct {
	http.ResponseWriter
	started bool
}

func (w *timeoutResponseWriter) WriteHeader(code int) {
	if !w.started {
		w.started = true
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *timeoutResponseWriter) Write(b []byte) (int, error) {
	if !w.started {
		w.started = true
		w.ResponseWriter.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}
