package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// requestIDKey é a chave usada para armazenar o request ID no contexto.
type requestIDKey struct{}

// RequestID injeta um cabeçalho X-Request-ID único em cada requisição,
// permitindo rastreamento de ponta a ponta em logs distribuídos.
// Se a requisição já vier com X-Request-ID, esse valor é preservado.
func RequestID(headerName string) func(http.Handler) http.Handler {
	if headerName == "" {
		headerName = "X-Request-ID"
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := r.Header.Get(headerName)
			if requestID == "" {
				requestID = newRequestID()
			}

			// Armazenar no contexto da requisição para uso posterior pelos handlers
			ctx := context.WithValue(r.Context(), requestIDKey{}, requestID)
			r = r.WithContext(ctx)

			// Definir no header de resposta para o cliente conseguir rastrear
			w.Header().Set(headerName, requestID)

			next.ServeHTTP(w, r)
		})
	}
}

// RequestIDFromContext extrai o request ID de um contexto de requisição.
func RequestIDFromContext(r *http.Request) string {
	if v := r.Context().Value(requestIDKey{}); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func newRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "req-00000000-0000-0000-0000-000000000000"
	}
	return hex.EncodeToString(b[:4]) + "-" +
		hex.EncodeToString(b[4:6]) + "-" +
		hex.EncodeToString(b[6:8]) + "-" +
		hex.EncodeToString(b[8:10]) + "-" +
		hex.EncodeToString(b[10:])
}
