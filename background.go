package gofast

import (
	"context"
	"log/slog"
	"sync"
)

// BackgroundTask representa uma tarefa executada depois que a resposta HTTP
// foi escrita. Tarefas são in-process e não oferecem persistência durável.
type BackgroundTask func(context.Context) error

// BackgroundTasks acumula tarefas associadas a uma única requisição.
type BackgroundTasks struct {
	mu    sync.Mutex
	tasks []BackgroundTask
}

// AddTask registra uma tarefa para execução após o commit da resposta.
func (b *BackgroundTasks) AddTask(task BackgroundTask) {
	if b == nil || task == nil {
		return
	}
	b.mu.Lock()
	b.tasks = append(b.tasks, task)
	b.mu.Unlock()
}

// Add registra uma função sem retorno de erro como tarefa pós-resposta.
func (b *BackgroundTasks) Add(task func()) {
	if task == nil {
		return
	}
	b.AddTask(func(context.Context) error { task(); return nil })
}

func (b *BackgroundTasks) run(ctx context.Context, logger *slog.Logger) {
	if b == nil {
		return
	}
	b.mu.Lock()
	tasks := append([]BackgroundTask(nil), b.tasks...)
	b.tasks = nil
	b.mu.Unlock()

	if logger == nil {
		logger = slog.Default()
	}
	for _, task := range tasks {
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.Error("GoFastAPI background task panic", "panic", recovered)
				}
			}()
			if err := task(ctx); err != nil {
				logger.Error("GoFastAPI background task failed", "error", err)
			}
		}()
	}
}
