package gofast

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// HealthStatus representa o status de saúde da aplicação.
type HealthStatus string

const (
	// HealthStatusLive indica que a aplicação está viva (processo rodando).
	HealthStatusLive HealthStatus = "live"
	// HealthStatusReady indica que a aplicação está pronta para receber tráfego.
	HealthStatusReady HealthStatus = "ready"
)

// HealthChecker é uma função que verifica a saúde de um componente.
type HealthChecker func() error

// HealthManager gerencia health checks da aplicação.
type HealthManager struct {
	mu        sync.RWMutex
	checkers  map[string]HealthChecker
	live      bool
	ready     bool
	startTime time.Time
}

// NewHealthManager cria um novo gerenciador de health checks.
func NewHealthManager() *HealthManager {
	return &HealthManager{
		checkers:  make(map[string]HealthChecker),
		live:      true,
		ready:     false,
		startTime: time.Now(),
	}
}

// RegisterChecker registra um health checker para um componente.
func (h *HealthManager) RegisterChecker(name string, checker HealthChecker) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checkers[name] = checker
}

// SetReady define se a aplicação está pronta para receber tráfego.
func (h *HealthManager) SetReady(ready bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ready = ready
}

// IsReady retorna se a aplicação está pronta.
func (h *HealthManager) IsReady() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.ready
}

// IsLive retorna se a aplicação está viva.
func (h *HealthManager) IsLive() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.live
}

// Check executa todos os health checks registrados.
func (h *HealthManager) Check() map[string]error {
	h.mu.RLock()
	checkers := make(map[string]HealthChecker, len(h.checkers))
	for k, v := range h.checkers {
		checkers[k] = v
	}
	h.mu.RUnlock()

	results := make(map[string]error)
	for name, checker := range checkers {
		results[name] = checker()
	}
	return results
}

// HealthResponse representa a resposta de um health check.
type HealthResponse struct {
	Status    string            `json:"status"`
	Uptime    string            `json:"uptime"`
	Checks    map[string]string `json:"checks,omitempty"`
	Timestamp string            `json:"timestamp"`
}

// HealthHandler retorna um handler HTTP para health checks.
func (h *HealthManager) HealthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.mu.RLock()
		live := h.live
		ready := h.ready
		startTime := h.startTime
		h.mu.RUnlock()

		checks := h.Check()

		checkResults := make(map[string]string)
		allHealthy := true
		for name, err := range checks {
			if err != nil {
				checkResults[name] = "unhealthy: " + err.Error()
				allHealthy = false
			} else {
				checkResults[name] = "healthy"
			}
		}

		status := "ok"
		if !live {
			status = "dead"
			allHealthy = false
		} else if !ready {
			status = "not ready"
			allHealthy = false
		} else if !allHealthy {
			status = "degraded"
		}

		response := HealthResponse{
			Status:    status,
			Uptime:    time.Since(startTime).String(),
			Checks:    checkResults,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}

		w.Header().Set("Content-Type", "application/json")
		if status == "ok" {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		json.NewEncoder(w).Encode(response)
	}
}

// LiveHandler retorna um handler para liveness check (simples, sem dependências externas).
func (h *HealthManager) LiveHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.mu.RLock()
		live := h.live
		h.mu.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		if live {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "live"})
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"status": "dead"})
		}
	}
}

// ReadyHandler retorna um handler para readiness check (verifica dependências).
func (h *HealthManager) ReadyHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.mu.RLock()
		ready := h.ready
		h.mu.RUnlock()

		checks := h.Check()

		checkResults := make(map[string]string)
		allHealthy := true
		for name, err := range checks {
			if err != nil {
				checkResults[name] = "unhealthy: " + err.Error()
				allHealthy = false
			} else {
				checkResults[name] = "healthy"
			}
		}

		w.Header().Set("Content-Type", "application/json")
		if ready && allHealthy {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{
				"status": "not ready",
				"checks": "see details",
			})
		}
	}
}

var _ = time.Second
