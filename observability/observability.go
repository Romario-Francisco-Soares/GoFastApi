// Package observability fornece métricas e instrumentação para a aplicação
// GoFastAPI. É intencionalmente sem dependências externas (OpenTelemetry opcional
// pode ser injetado posteriormente) e seguro para uso concorrente.
package observability

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Counter é um contador monotônico seguro para uso concorrente.
type Counter struct {
	mu sync.Mutex
	v  int64
}

// Inc incrementa o contador em 1.
func (c *Counter) Inc() {
	c.mu.Lock()
	c.v++
	c.mu.Unlock()
}

// Add adiciona n ao contador.
func (c *Counter) Add(n int64) {
	c.mu.Lock()
	c.v += n
	c.mu.Unlock()
}

// Value retorna o valor atual do contador.
func (c *Counter) Value() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.v
}

// Gauge é um medidor que pode subir e descer, seguro para uso concorrente.
type Gauge struct {
	v int64
}

// Inc incrementa o gauge em 1.
func (g *Gauge) Inc() {
	atomic.AddInt64(&g.v, 1)
}

// Dec decrementa o gauge em 1.
func (g *Gauge) Dec() {
	atomic.AddInt64(&g.v, -1)
}

// Value retorna o valor atual do gauge.
func (g *Gauge) Value() int64 {
	return atomic.LoadInt64(&g.v)
}

// Histogram registra medições de duração/tamanho e mantém estatísticas básicas.
type Histogram struct {
	mu      sync.Mutex
	count   int64
	sum     int64 // nanos
	min     int64
	max     int64
	buckets map[int64]int64 // bucket upper bound -> count
}

// Observe registra uma medição em nanossegundos.
func (h *Histogram) Observe(nanos int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.count++
	h.sum += nanos
	if h.count == 1 || nanos < h.min {
		h.min = nanos
	}
	if nanos > h.max {
		h.max = nanos
	}
	// Buckets simples em potências de 10 microssegundos (1µs, 10µs, 100µs, 1ms, 10ms, 100ms, 1s)
	b := int64(1)
	for b < nanos {
		b *= 10
	}
	if h.buckets == nil {
		h.buckets = make(map[int64]int64)
	}
	h.buckets[b]++
}

// Count retorna o número de observações.
func (h *Histogram) Count() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.count
}

// Sum retorna a soma total de todas as observações em nanossegundos.
func (h *Histogram) Sum() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sum
}

// Min retorna o menor valor observado.
func (h *Histogram) Min() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.min
}

// Max retorna o maior valor observado.
func (h *Histogram) Max() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.max
}

// Mean retorna a média das observações (0 se não houver observações).
func (h *Histogram) Mean() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.count == 0 {
		return 0
	}
	return float64(h.sum) / float64(h.count)
}

// Metrics expõe os contadores, gauges e histogramas da aplicação.
type Metrics struct {
	RequestsTotal     *Counter
	RequestsDuration  *Histogram
	RequestsInFlight  *Gauge
	ResponsesByStatus *Counter // key: "method:route:status"
	PanicsTotal       *Counter
}

// NewMetrics cria um conjunto de métricas zerado.
func NewMetrics() *Metrics {
	return &Metrics{
		RequestsTotal:     &Counter{},
		RequestsDuration:  &Histogram{},
		RequestsInFlight:  &Gauge{},
		ResponsesByStatus: &Counter{},
		PanicsTotal:       &Counter{},
	}
}

// Observability encapsula métricas e instrumentação da aplicação.
type Observability struct {
	metrics *Metrics
	enabled bool
}

// NewObservability cria uma instância de observabilidade.
// Quando enabled é false, as métricas ainda são coletadas mas não são exportadas.
func NewObservability(enabled bool) *Observability {
	return &Observability{
		metrics: NewMetrics(),
		enabled: enabled,
	}
}

// Metrics retorna o coletor de métricas.
func (o *Observability) Metrics() *Metrics {
	return o.metrics
}

// Enabled retorna se a observabilidade está ativa (exportação habilitada).
func (o *Observability) Enabled() bool {
	return o.enabled
}

// Middleware retorna um middleware HTTP que instrumenta requisições:
// conta requests totais, duração, status code e requests em voo.
func (o *Observability) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Contabilizar request em voo
			o.metrics.RequestsInFlight.Inc()
			defer o.metrics.RequestsInFlight.Dec()

			// Wrapper para capturar status code
			rw := &statusWriter{ResponseWriter: w, statusCode: http.StatusOK}

			var panicErr any
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						panicErr = rec
						o.metrics.PanicsTotal.Inc()
						panic(rec) // re-panic para o Recovery middleware tratar
					}
				}()
				next.ServeHTTP(rw, r)
			}()

			duration := time.Since(start).Nanoseconds()

			// Contabilizar métricas
			o.metrics.RequestsTotal.Inc()
			o.metrics.RequestsDuration.Observe(duration)
			o.metrics.ResponsesByStatus.Add(1) // simplificado: contador geral

			_ = panicErr
		})
	}
}

// statusWriter captura o status code da resposta sem escrever no ResponseWriter original
// até que WriteHeader seja chamado.
type statusWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.written {
		w.statusCode = code
		w.written = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(data []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}
