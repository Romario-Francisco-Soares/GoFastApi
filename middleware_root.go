package gofast

import (
	"log/slog"
	"net/http"
	"time"

	"gofast/middleware"
)

// Recovery recupera panics ocorridos durante o tratamento de uma requisição
// e retorna uma resposta HTTP 500 Interno, sem expor stack traces ao cliente.
// O stack trace é enviado ao logger para diagnóstico interno.
// É um wrapper sobre middleware.Recovery para exposição no pacote raiz.
func Recovery(logger *slog.Logger) Middleware {
	return middleware.Recovery(logger)
}

// RequestID injeta um cabeçalho X-Request-ID único em cada requisição,
// permitindo rastreamento de ponta a ponta em logs distribuídos.
// Se a requisição já vier com X-Request-ID, esse valor é preservado.
// É um wrapper sobre middleware.RequestID para exposição no pacote raiz.
func RequestID(headerName string) Middleware {
	return middleware.RequestID(headerName)
}

// RequestIDFromContext extrai o request ID de um contexto de requisição.
func RequestIDFromContext(r *http.Request) string {
	return middleware.RequestIDFromContext(r)
}

// CORSConfig configura o middleware de CORS (Cross-Origin Resource Sharing).
// É um alias sobre middleware.CORSConfig para exposição no pacote raiz.
type CORSConfig = middleware.CORSConfig

// CORS aplica CORS com validação de origem e preflight.
// É um wrapper sobre middleware.CORS para exposição no pacote raiz.
func CORS(config CORSConfig) Middleware {
	return middleware.CORS(config)
}

// Compression implementa compressão gzip de respostas,
// equivalente ao GZipMiddleware do FastAPI.
// É um wrapper sobre middleware.Compression para exposição no pacote raiz.
func Compression() Middleware {
	return middleware.Compression()
}

// Timeout limita o tempo de execução de um handler.
// Se o handler não finalizar dentro do tempo limite, retorna 503 Service Unavailable.
// É um wrapper sobre middleware.Timeout para exposição no pacote raiz.
func Timeout(d time.Duration) Middleware {
	return middleware.Timeout(d)
}
