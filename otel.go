package gofast

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
)

// TracerConfig configura o rastreamento OpenTelemetry.
type TracerConfig struct {
	ServiceName    string
	ServiceVersion string
	Enabled        bool
}

// MeterConfig configura as métricas OpenTelemetry.
type MeterConfig struct {
	Enabled bool
}

// TelemetryConfig configura o OpenTelemetry completo.
type TelemetryConfig struct {
	Tracer TracerConfig
	Meter  MeterConfig
}

// OTelManager gerencia instrumentação OpenTelemetry.
type OTelManager struct {
	mu      sync.RWMutex
	config  TelemetryConfig
	tracer  *noopTracer
	meter   *noopMeter
	logger  *slog.Logger
	enabled bool
}

// noopTracer é um tracer sem operação quando OTel não está disponível.
type noopTracer struct{}

// Start inicia um span sem operação.
func (t *noopTracer) Start(ctx context.Context, name string, opts ...any) (context.Context, *noopSpan) {
	return ctx, &noopSpan{}
}

// noopSpan é um span sem operação.
type noopSpan struct{}

// End finaliza o span sem operação.
func (s *noopSpan) End(opts ...any) {}

// SetAttribute define um atributo sem operação.
func (s *noopSpan) SetAttribute(key string, value any) {}

// noopMeter é um medidor sem operação.
type noopMeter struct{}

// Counter cria um contador sem operação.
func (m *noopMeter) Counter(name string, opts ...any) *noopCounter {
	return &noopCounter{}
}

// noopCounter é um contador sem operação.
type noopCounter struct{}

// Add adiciona um valor ao contador sem operação.
func (c *noopCounter) Add(value float64, opts ...any) {}

// NewOTelManager cria um novo gerenciador OpenTelemetry.
func NewOTelManager(config TelemetryConfig, logger *slog.Logger) *OTelManager {
	if logger == nil {
		logger = slog.Default()
	}

	return &OTelManager{
		config:  config,
		tracer:  &noopTracer{},
		meter:   &noopMeter{},
		logger:  logger,
		enabled: config.Tracer.Enabled || config.Meter.Enabled,
	}
}

// Tracer retorna o tracer configurado.
func (o *OTelManager) Tracer() *noopTracer {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.tracer
}

// Meter retorna o medidor configurado.
func (o *OTelManager) Meter() *noopMeter {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.meter
}

// Enabled retorna se a instrumentação está ativa.
func (o *OTelManager) Enabled() bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.enabled
}

// Middleware retorna um middleware HTTP que instrumenta requisições com traces.
func (o *OTelManager) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !o.enabled {
				next.ServeHTTP(w, r)
				return
			}

			// Extrair contexto de trace do header W3C
			ctx := r.Context()
			traceID := r.Header.Get("traceparent")
			if traceID != "" {
				// Em implementação completa, parsearíamos o traceparent
				// Aqui apenas propagamos o header
				ctx = context.WithValue(ctx, "traceparent", traceID)
			}

			// Iniciar span da requisição
			_, span := o.Tracer().Start(ctx, "HTTP "+r.Method+" "+r.URL.Path)
			defer span.End()

			// Definir atributos do span
			span.SetAttribute("http.method", r.Method)
			span.SetAttribute("http.url", r.URL.String())
			span.SetAttribute("http.route", r.URL.Path)

			// Wrapper para capturar status code
			rw := &responseWriterWrapper{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			next.ServeHTTP(rw, r)

			span.SetAttribute("http.status_code", rw.statusCode)
		})
	}
}

// responseWriterWrapper captura o status code da resposta.
type responseWriterWrapper struct {
	http.ResponseWriter
	statusCode int
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
	return n, err
}

// TraceMiddleware é um middleware de trace para uso direto.
func TraceMiddleware(config TelemetryConfig, logger *slog.Logger) func(http.Handler) http.Handler {
	otel := NewOTelManager(config, logger)
	return otel.Middleware()
}

// ExtractTraceContext extrai o contexto de trace de um header HTTP.
func ExtractTraceContext(r *http.Request) map[string]string {
	ctx := make(map[string]string)

	traceparent := r.Header.Get("traceparent")
	if traceparent != "" {
		ctx["traceparent"] = traceparent
	}

	tracestate := r.Header.Get("tracestate")
	if tracestate != "" {
		ctx["tracestate"] = tracestate
	}

	return ctx
}

// InjectTraceContext injeta o contexto de trace em um header HTTP.
func InjectTraceContext(r *http.Request, ctx map[string]string) {
	if v, ok := ctx["traceparent"]; ok {
		r.Header.Set("traceparent", v)
	}
	if v, ok := ctx["tracestate"]; ok {
		r.Header.Set("tracestate", v)
	}
}

// W3C traceparent header format: version-traceid-spanid-traceflags
const (
	traceVersion = "00"
)

// GenerateTraceID gera um trace ID simples (para desenvolvimento sem OTel completo).
func GenerateTraceID() string {
	return traceVersion + "-" + generateRandomHex(32) + "-" + generateRandomHex(16) + "-01"
}

func generateRandomHex(n int) string {
	const hexChars = "0123456789abcdef"
	result := make([]byte, n)
	for i := 0; i < n; i++ {
		result[i] = hexChars[(i*7+3)%len(hexChars)] // Determinístico para simplicidade
	}
	return string(result)
}

var _ = strings.TrimSpace
var _ = http.StatusOK
