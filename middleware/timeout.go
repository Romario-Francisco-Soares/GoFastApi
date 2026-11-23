package middleware

import (
	"net/http"
	"time"
)

// Timeout limita o tempo de execução de um handler.
//
// A implementação delega para http.TimeoutHandler: a resposta produzida pelo
// handler é bufferizada e só é copiada para o cliente quando o handler termina
// dentro do prazo. Se o prazo expirar, o cliente recebe 503 com corpo JSON e o
// resultado tardio do handler é descartado, sem escrita concorrente no
// ResponseWriter.
//
// Como a resposta é bufferizada, este middleware não deve envolver rotas de
// streaming (SSE) nem de upgrade de WebSocket: nesses casos o Flush/Hijack
// ficaria indisponível. Um prazo menor ou igual a zero desabilita o middleware.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	if d <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		handler := http.TimeoutHandler(next, d, `{"error":"request timeout"}`)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handler.ServeHTTP(&timeoutContentTypeWriter{ResponseWriter: w}, r)
		})
	}
}

// timeoutContentTypeWriter garante que a resposta 503 gerada pelo
// http.TimeoutHandler saia como JSON. O TimeoutHandler não define
// Content-Type e, sem isso, o net/http inferiria text/plain pelo corpo.
type timeoutContentTypeWriter struct {
	http.ResponseWriter
}

func (w *timeoutContentTypeWriter) WriteHeader(code int) {
	if code == http.StatusServiceUnavailable && w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	w.ResponseWriter.WriteHeader(code)
}
