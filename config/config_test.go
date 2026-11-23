package config

import (
	"os"
	"path/filepath"
	"testing"
)

// ---------- Defaults ----------

func TestLoader_Defaults(t *testing.T) {
	cfg, err := NewLoader().Load()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Addr != ":8000" {
		t.Fatalf("addr padrão=%q, want :8000", cfg.Addr)
	}
	if cfg.Workers != 1 {
		t.Fatalf("workers padrão=%d, want 1", cfg.Workers)
	}
	if cfg.LogLevel != "info" {
		t.Fatalf("log level padrão=%q, want info", cfg.LogLevel)
	}
	if cfg.AccessLog != true {
		t.Fatal("access log padrão deveria ser true")
	}
}

// ---------- Environment variables ----------

func TestLoader_EnvPrefix(t *testing.T) {
	// Definir variáveis de ambiente
	os.Setenv("GOFAST_ADDR", ":9000")
	os.Setenv("GOFAST_WORKERS", "4")
	defer func() {
		os.Unsetenv("GOFAST_ADDR")
		os.Unsetenv("GOFAST_WORKERS")
	}()

	cfg, err := NewLoader().Load()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Addr != ":9000" {
		t.Fatalf("addr do env=%q, want :9000", cfg.Addr)
	}
	if cfg.Workers != 4 {
		t.Fatalf("workers do env=%d, want 4", cfg.Workers)
	}
}

// ---------- Config file ----------

func TestLoader_ConfigFile(t *testing.T) {
	// Criar arquivo JSON temporário
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"Addr": ":7000",
		"Workers": 2,
		"LogLevel": "warn",
		"AccessLog": false,
		"MaxBodyBytes": 5242880
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := NewLoader(WithConfigFile(path)).Load()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Addr != ":7000" {
		t.Fatalf("addr do arquivo=%q, want :7000", cfg.Addr)
	}
	if cfg.Workers != 2 {
		t.Fatalf("workers do arquivo=%d, want 2", cfg.Workers)
	}
	if cfg.LogLevel != "warn" {
		t.Fatalf("log level do arquivo=%q, want warn", cfg.LogLevel)
	}
	if cfg.AccessLog != false {
		t.Fatal("access log do arquivo deveria ser false")
	}
	if cfg.MaxBodyBytes != 5242880 {
		t.Fatalf("max body do arquivo=%d, want 5242880", cfg.MaxBodyBytes)
	}
}

// ---------- Precedence: file overrides defaults ----------

func TestLoader_Precedence_FileOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"Workers": 5}`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := NewLoader(WithConfigFile(path)).Load()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Workers != 5 {
		t.Fatalf("workers com arquivo=%d, want 5 (override de default)", cfg.Workers)
	}
	// Outros campos devem continuar com defaults
	if cfg.Addr != ":8000" {
		t.Fatalf("addr sem arquivo=%q, want :8000", cfg.Addr)
	}
}

// ---------- Precedence: env overrides file ----------

func TestLoader_Precedence_EnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"Addr": ":7000", "Workers": 2}`), 0644); err != nil {
		t.Fatal(err)
	}

	os.Setenv("GOFAST_ADDR", ":9999")
	defer os.Unsetenv("GOFAST_ADDR")

	cfg, err := NewLoader(WithConfigFile(path)).Load()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Addr != ":9999" {
		t.Fatalf("addr com env override=%q, want :9999", cfg.Addr)
	}
	// Workers deve vir do arquivo (não sobrescrito por env)
	if cfg.Workers != 2 {
		t.Fatalf("workers sem env override=%d, want 2", cfg.Workers)
	}
}

// ---------- Precedence: CLI overrides env ----------

func TestLoader_Precedence_CLIOverridesEnv(t *testing.T) {
	os.Setenv("GOFAST_ADDR", ":8888")
	defer os.Unsetenv("GOFAST_ADDR")

	cfg, err := NewLoader(WithCLIOverrides(map[string]any{
		"Addr": ":5555",
	})).Load()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Addr != ":5555" {
		t.Fatalf("addr com CLI override=%q, want :5555", cfg.Addr)
	}
}

// ---------- Precedence: CLI overrides file ----------

func TestLoader_Precedence_CLIOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"Addr": ":7000"}`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := NewLoader(
		WithConfigFile(path),
		WithCLIOverrides(map[string]any{"Addr": ":4444"}),
	).Load()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Addr != ":4444" {
		t.Fatalf("addr com CLI sobre arquivo=%q, want :4444", cfg.Addr)
	}
}

// ---------- Custom env prefix ----------

func TestLoader_CustomEnvPrefix(t *testing.T) {
	os.Setenv("MYAPP_ADDR", ":6000")
	defer os.Unsetenv("MYAPP_ADDR")

	cfg, err := NewLoader(WithEnvPrefix("MYAPP_")).Load()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Addr != ":6000" {
		t.Fatalf("addr com prefixo custom=%q, want :6000", cfg.Addr)
	}
}

// ---------- Multiple env vars ----------

func TestLoader_MultipleEnvVars(t *testing.T) {
	os.Setenv("GOFAST_ADDR", ":1000")
	os.Setenv("GOFAST_PORT", "8080")
	os.Setenv("GOFAST_GRACEFUL_TIMEOUT", "45s") // duração válida (ex: 45s)
	defer func() {
		os.Unsetenv("GOFAST_ADDR")
		os.Unsetenv("GOFAST_PORT")
		os.Unsetenv("GOFAST_GRACEFUL_TIMEOUT")
	}()

	// GOFAST_PORT não existe em ServerConfig e é ignorado silenciosamente.
	// GOFAST_GRACEFUL_TIMEOUT é uma duração válida ("45s") e passa a ser aplicada.
	cfg, err := NewLoader().Load()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Addr != ":1000" {
		t.Fatalf("addr com múltiplos env=%q, want :1000", cfg.Addr)
	}
}

// ---------- LoadDefault ----------

func TestLoadDefault(t *testing.T) {
	cfg := LoadDefault()
	if cfg.Addr != ":8000" {
		t.Fatalf("load default addr=%q, want :8000", cfg.Addr)
	}
	if cfg.Workers != 1 {
		t.Fatalf("load default workers=%d, want 1", cfg.Workers)
	}
}

// ---------- MustLoad ----------

func TestMustLoad(t *testing.T) {
	cfg := MustLoad()
	if cfg.Addr != ":8000" {
		t.Fatalf("must load addr=%q, want :8000", cfg.Addr)
	}
}

func TestMustLoad_PanicsOnError(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("esperava panic com arquivo inexistente")
		}
	}()

	MustLoad(WithConfigFile("/caminho/inexistente/config.json"))
}
