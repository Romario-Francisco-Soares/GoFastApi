// Package hotreload fornece recarga automática do servidor quando arquivos
// de código-fonte são modificados. Útil para desenvolvimento (modo dev).
package hotreload

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// HotReloader monitora diretórios e reinicia o servidor quando arquivos .go
// são modificados.
type HotReloader struct {
	app        App
	dirs       []string
	delay      time.Duration
	mu         sync.RWMutex
	running    bool
	stopChan   chan struct{}
	restartCh  chan struct{} // fecha quando uma reinicialização é solicitada
	lastChange map[string]time.Time
}

// NewHotReloader cria um novo HotReloader.
func NewHotReloader(app App, dirs []string, delay time.Duration) *HotReloader {
	return &HotReloader{
		app:        app,
		dirs:       dirs,
		delay:      delay,
		stopChan:   make(chan struct{}),
		restartCh:  make(chan struct{}, 1),
		lastChange: make(map[string]time.Time),
	}
}

// Start inicia o monitoramento de arquivos em uma goroutine separada.
func (h *HotReloader) Start() error {
	h.mu.Lock()
	if h.running {
		h.mu.Unlock()
		return fmt.Errorf("hot reloader already running")
	}
	h.running = true
	h.mu.Unlock()

	go h.watchLoop()
	return nil
}

// Stop para o monitoramento.
func (h *HotReloader) Stop() error {
	h.mu.Lock()
	if !h.running {
		h.mu.Unlock()
		return nil
	}
	h.running = false
	h.mu.Unlock()

	close(h.stopChan)
	return nil
}

// RestartRequested retorna um canal que fecha quando o hot reloader detecta
// uma mudança e solicita uma reinicialização do servidor.
// O processo pai deve monitorar este canal e reiniciar o servidor.
func (h *HotReloader) RestartRequested() <-chan struct{} {
	return h.restartCh
}

// watchLoop monitora os diretórios em busca de mudanças.
func (h *HotReloader) watchLoop() {
	ticker := time.NewTicker(h.delay)
	defer ticker.Stop()

	for {
		select {
		case <-h.stopChan:
			return
		case <-ticker.C:
			h.checkForChanges()
		case <-h.restartCh:
			// Canal já foi sinalizado, apenas aguardar próximo ciclo
		}
	}
}

// checkForChanges verifica se algum arquivo .go foi modificado.
func (h *HotReloader) checkForChanges() {
	for _, dir := range h.dirs {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}

			info, err := d.Info()
			if err != nil {
				return nil
			}

			modTime := info.ModTime()
			lastChange, exists := h.lastChange[path]
			if !exists {
				h.lastChange[path] = modTime
				return nil
			}

			if modTime.After(lastChange) {
				h.lastChange[path] = modTime
				h.triggerRestart()
			}
			return nil
		})
		if err != nil {
			// Ignorar erros de diretório
		}
	}
}

// triggerRestart solicita uma reinicialização do servidor.
func (h *HotReloader) triggerRestart() {
	select {
	case h.restartCh <- struct{}{}:
	default:
		// Canal cheio, reinício já agendado
	}
}

// IsRunning retorna se o hot reloader está ativo.
func (h *HotReloader) IsRunning() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.running
}

// App representa a interface necessária pelo HotReloader.
type App interface {
	Shutdown(ctx context.Context) error
}

// Garantir que o pacote compile corretamente com todos os imports usados.
var _ = filepath.Base
var _ = strings.TrimSpace
