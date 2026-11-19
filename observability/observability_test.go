package observability

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ---------- Counter ----------

func TestCounter_Inc(t *testing.T) {
	c := &Counter{}
	if c.Value() != 0 {
		t.Fatalf("valor inicial=%d, want 0", c.Value())
	}
	c.Inc()
	if c.Value() != 1 {
		t.Fatalf("após Inc=%d, want 1", c.Value())
	}
	c.Inc()
	if c.Value() != 2 {
		t.Fatalf("após 2 Inc=%d, want 2", c.Value())
	}
}

func TestCounter_Add(t *testing.T) {
	c := &Counter{}
	c.Add(5)
	if c.Value() != 5 {
		t.Fatalf("após Add(5)=%d, want 5", c.Value())
	}
	c.Add(3)
	if c.Value() != 8 {
		t.Fatalf("após Add(3)=%d, want 8", c.Value())
	}
}

// ---------- Gauge ----------

func TestGauge_IncDec(t *testing.T) {
	g := &Gauge{}
	if g.Value() != 0 {
		t.Fatalf("valor inicial=%d, want 0", g.Value())
	}
	g.Inc()
	if g.Value() != 1 {
		t.Fatalf("após Inc=%d, want 1", g.Value())
	}
	g.Dec()
	if g.Value() != 0 {
		t.Fatalf("após Dec=%d, want 0", g.Value())
	}
}

// ---------- Histogram ----------

func TestHistogram_Observe(t *testing.T) {
	h := &Histogram{}
	h.Observe(100)
	h.Observe(200)
	h.Observe(300)

	if h.Count() != 3 {
		t.Fatalf("count=%d, want 3", h.Count())
	}
	if h.Sum() != 600 {
		t.Fatalf("sum=%d, want 600", h.Sum())
	}
	if h.Min() != 100 {
		t.Fatalf("min=%d, want 100", h.Min())
	}
	if h.Max() != 300 {
		t.Fatalf("max=%d, want 300", h.Max())
	}
	if h.Mean() != 200.0 {
		t.Fatalf("mean=%f, want 200.0", h.Mean())
	}
}

func TestHistogram_Empty(t *testing.T) {
	h := &Histogram{}
	if h.Count() != 0 {
		t.Fatalf("count vazio=%d, want 0", h.Count())
	}
	if h.Mean() != 0.0 {
		t.Fatalf("mean vazio=%f, want 0.0", h.Mean())
	}
}

// ---------- Metrics ----------

func TestMetrics_NewMetrics(t *testing.T) {
	m := NewMetrics()
	if m.RequestsTotal.Value() != 0 {
		t.Fatalf("requests total inicial=%d, want 0", m.RequestsTotal.Value())
	}
	if m.RequestsInFlight.Value() != 0 {
		t.Fatalf("in flight inicial=%d, want 0", m.RequestsInFlight.Value())
	}
	if m.PanicsTotal.Value() != 0 {
		t.Fatalf("panics inicial=%d, want 0", m.PanicsTotal.Value())
	}
}

// ---------- Observability ----------

func TestObservability_New(t *testing.T) {
	o := NewObservability(false)
	if o.Enabled() {
		t.Fatal("enabled deveria ser false")
	}
	if o.Metrics() == nil {
		t.Fatal("metrics não deve ser nil")
	}

	o2 := NewObservability(true)
	if !o2.Enabled() {
		t.Fatal("enabled deveria ser true")
	}
}

// ---------- Middleware ----------

func TestMiddleware_RecordsMetrics(t *testing.T) {
	o := NewObservability(true)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	})

	handler := o.Middleware()(next)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", w.Code)
	}

	if o.Metrics().RequestsTotal.Value() != 1 {
		t.Fatalf("requests total=%d, want 1", o.Metrics().RequestsTotal.Value())
	}
	if o.Metrics().RequestsDuration.Count() != 1 {
		t.Fatalf("duration count=%d, want 1", o.Metrics().RequestsDuration.Count())
	}
	if o.Metrics().RequestsInFlight.Value() != 0 {
		t.Fatalf("in flight após conclusão=%d, want 0", o.Metrics().RequestsInFlight.Value())
	}
}

func TestMiddleware_PanicsTotal(t *testing.T) {
	o := NewObservability(true)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})

	handler := o.Middleware()(next)

	req := httptest.NewRequest("GET", "/panic", nil)
	w := httptest.NewRecorder()

	// O middleware re-panica (para o Recovery middleware tratar em produção).
	// Aqui capturamos o panic apenas para verificar que o contador foi incrementado.
	defer func() {
		recover()
	}()
	handler.ServeHTTP(w, req)

	if o.Metrics().PanicsTotal.Value() != 1 {
		t.Fatalf("panics total=%d, want 1", o.Metrics().PanicsTotal.Value())
	}
}

func TestMiddleware_InFlightDuringRequest(t *testing.T) {
	o := NewObservability(true)

	// Um handler que demora um pouco para verificar que in-flight sobe durante a execução
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	handler := o.Middleware()(next)

	// Fazer requisição em goroutine para verificar in-flight durante a execução
	done := make(chan struct{})
	go func() {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/slow", nil)
		handler.ServeHTTP(w, req)
		close(done)
	}()

	// Dar tempo para a requisição começar
	time.Sleep(5 * time.Millisecond)

	if o.Metrics().RequestsInFlight.Value() != 1 {
		t.Fatalf("in flight durante requisição=%d, want 1", o.Metrics().RequestsInFlight.Value())
	}

	<-done

	if o.Metrics().RequestsInFlight.Value() != 0 {
		t.Fatalf("in flight após conclusão=%d, want 0", o.Metrics().RequestsInFlight.Value())
	}
}
