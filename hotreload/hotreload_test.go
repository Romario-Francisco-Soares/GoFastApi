package hotreload

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mockApp é uma implementação mock da interface App para testes.
type mockApp struct {
	shutdownCalled bool
}

func (m *mockApp) Shutdown(ctx context.Context) error {
	m.shutdownCalled = true
	return nil
}

func TestHotReloader_New(t *testing.T) {
	app := &mockApp{}
	reloader := NewHotReloader(app, []string{"."}, 1*time.Second)
	if reloader == nil {
		t.Fatal("NewHotReloader returned nil")
	}
	if reloader.IsRunning() {
		t.Fatal("IsRunning() should be false before Start()")
	}
}

func TestHotReloader_StartStop(t *testing.T) {
	app := &mockApp{}
	reloader := NewHotReloader(app, []string{"."}, 100*time.Millisecond)
	if err := reloader.Start(); err != nil {
		t.Fatalf("Start() returned error: %v", err)
	}
	if !reloader.IsRunning() {
		t.Fatal("IsRunning() should be true after Start()")
	}
	if err := reloader.Stop(); err != nil {
		t.Fatalf("Stop() returned error: %v", err)
	}
	if reloader.IsRunning() {
		t.Fatal("IsRunning() should be false after Stop()")
	}
}

func TestHotReloader_StartTwice(t *testing.T) {
	app := &mockApp{}
	reloader := NewHotReloader(app, []string{"."}, 100*time.Millisecond)
	if err := reloader.Start(); err != nil {
		t.Fatalf("Start() returned error: %v", err)
	}
	if err := reloader.Start(); err == nil {
		t.Fatal("Start() should return error when already running")
	}
	reloader.Stop()
}

func TestHotReloader_DetectsFileChange(t *testing.T) {
	// Criar diretório temporário para teste
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.go")

	// Criar arquivo .go inicial
	if err := os.WriteFile(testFile, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	app := &mockApp{}
	reloader := NewHotReloader(app, []string{tmpDir}, 50*time.Millisecond)
	if err := reloader.Start(); err != nil {
		t.Fatalf("Start() returned error: %v", err)
	}
	defer reloader.Stop()

	// Aguardar o leitor inicializar (pegar mtime inicial)
	time.Sleep(100 * time.Millisecond)

	// Modificar o arquivo
	if err := os.WriteFile(testFile, []byte("package main\n\nfunc main() {}\n"), 0644); err != nil {
		t.Fatalf("Failed to modify test file: %v", err)
	}

	// Aguardar o watcher detectar a mudança
	time.Sleep(200 * time.Millisecond)

	// Verificar que a mudança foi detectada (última mudança atualizada)
	reloader.mu.RLock()
	lastChange, exists := reloader.lastChange[testFile]
	reloader.mu.RUnlock()
	if !exists {
		t.Fatal("lastChange should contain the test file after detection")
	}
	_ = lastChange // evitar variável não utilizada
}

func TestHotReloader_IgnoreNonGoFiles(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")

	if err := os.WriteFile(testFile, []byte("not go code\n"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	app := &mockApp{}
	reloader := NewHotReloader(app, []string{tmpDir}, 50*time.Millisecond)
	if err := reloader.Start(); err != nil {
		t.Fatalf("Start() returned error: %v", err)
	}
	defer reloader.Stop()

	// Aguardar inicialização
	time.Sleep(100 * time.Millisecond)

	// Modificar arquivo .txt (não deve ser detectado)
	if err := os.WriteFile(testFile, []byte("still not go code\n"), 0644); err != nil {
		t.Fatalf("Failed to modify test file: %v", err)
	}

	// Aguardar tempo suficiente para o watcher verificar
	time.Sleep(200 * time.Millisecond)

	// Verificar que o arquivo .txt NÃO está no lastChange
	reloader.mu.RLock()
	_, exists := reloader.lastChange[testFile]
	reloader.mu.RUnlock()
	if exists {
		t.Fatal("lastChange should not contain .txt files")
	}
}
