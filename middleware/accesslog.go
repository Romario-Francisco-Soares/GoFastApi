package middleware

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// AccessLogConfig configura o middleware de access log.
type AccessLogConfig struct {
	Logger     *slog.Logger
	Writer     io.Writer
	Levels     slog.Level
	SkipPaths  []string // caminhos a não logar (ex: /health)
	SkipStatus []int    // status codes a não logar
	Colors     bool     // usar cores no terminal (dev mode)
}

// AccessLog middleware registra logs estruturados de cada requisição (equivalente ao access log do Uvicorn).
func AccessLog(config AccessLogConfig) func(http.Handler) http.Handler {
	logger := config.Logger
	if logger == nil {
		if config.Writer == nil {
			config.Writer = io.Discard
		}
		level := config.Levels
		if level == 0 {
			level = slog.LevelInfo
		}
		opts := &slog.HandlerOptions{
			Level: level,
		}
		handler := slog.NewTextHandler(config.Writer, opts)
		logger = slog.New(handler)
	}

	skipPaths := make(map[string]bool)
	for _, p := range config.SkipPaths {
		skipPaths[p] = true
	}

	skipStatus := make(map[int]bool)
	for _, s := range config.SkipStatus {
		skipStatus[s] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Capturar status code via response writer wrapper
			rw := &responseWriterWrapper{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			next.ServeHTTP(rw, r)

			duration := time.Since(start)

			// Verificar se deve pular o log
			if skipPaths[r.URL.Path] {
				return
			}
			if skipStatus[rw.statusCode] {
				return
			}

			// Obter IP do cliente
			clientIP := ""
			if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
				clientIP = ip
			}

			// Não logar Authorization, Cookie ou bodies por padrão
			attrs := []any{
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("query", r.URL.RawQuery),
				slog.Int("status", rw.statusCode),
				slog.String("duration", duration.String()),
				slog.String("client_ip", clientIP),
				slog.String("user_agent", r.UserAgent()),
				slog.String("referer", r.Referer()),
				slog.Int("size", rw.size),
			}

			// Adicionar request_id se presente
			if reqID := r.Header.Get("X-Request-ID"); reqID != "" {
				attrs = append(attrs, slog.String("request_id", reqID))
			}

			// Determinar nível de log baseado no status
			level := slog.LevelInfo
			if rw.statusCode >= 500 {
				level = slog.LevelError
			} else if rw.statusCode >= 400 {
				level = slog.LevelWarn
			}

			// Formato de mensagem similar ao Uvicorn
			msg := fmt.Sprintf("%s %s - %d", r.Method, r.URL.Path, rw.statusCode)

			logger.Log(r.Context(), level, msg, attrs...)
		})
	}
}

// responseWriterWrapper captura o status code e tamanho da resposta.
type responseWriterWrapper struct {
	http.ResponseWriter
	statusCode int
	size       int
	written    bool
}

func (w *responseWriterWrapper) WriteHeader(code int) {
	w.statusCode = code
	w.written = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriterWrapper) Write(data []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	w.size += n
	return n, err
}
