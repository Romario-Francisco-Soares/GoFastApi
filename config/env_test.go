package config

import (
	"testing"
	"time"
)

func TestToEnvName(t *testing.T) {
	cases := map[string]string{
		"Addr":              "ADDR",
		"Workers":           "WORKERS",
		"LogLevel":          "LOG_LEVEL",
		"AccessLog":         "ACCESS_LOG",
		"ReadHeaderTimeout": "READ_HEADER_TIMEOUT",
		"MaxBodyBytes":      "MAX_BODY_BYTES",
		"PIDFile":           "PID_FILE",
		"TLSMinVersion":     "TLS_MIN_VERSION",
		"GracefulTimeout":   "GRACEFUL_TIMEOUT",
	}
	for field, want := range cases {
		if got := toEnvName(field); got != want {
			t.Errorf("toEnvName(%q) = %q, want %q", field, got, want)
		}
	}
}

func TestLoader_CanonicalSnakeCaseEnvName(t *testing.T) {
	// O nome canônico documentado é GOFAST_LOG_LEVEL (e não GOFAST_LogLevel).
	t.Setenv("GOFAST_LOG_LEVEL", "debug")

	cfg, err := NewLoader().Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("LogLevel = %q, want debug", cfg.LogLevel)
	}
}

func TestLoader_DurationEnv(t *testing.T) {
	t.Setenv("GOFAST_GRACEFUL_TIMEOUT", "45s")
	t.Setenv("GOFAST_READ_HEADER_TIMEOUT", "7s")
	t.Setenv("GOFAST_TIMEOUT_NOTIFY", "1500ms")

	cfg, err := NewLoader().Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GracefulTimeout != 45*time.Second {
		t.Errorf("GracefulTimeout = %v, want 45s", cfg.GracefulTimeout)
	}
	if cfg.ReadHeaderTimeout != 7*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want 7s", cfg.ReadHeaderTimeout)
	}
	if cfg.TimeoutNotify != 1500*time.Millisecond {
		t.Errorf("TimeoutNotify = %v, want 1.5s", cfg.TimeoutNotify)
	}
}

func TestLoader_InvalidDurationEnvFails(t *testing.T) {
	t.Setenv("GOFAST_GRACEFUL_TIMEOUT", "not-a-duration")

	if _, err := NewLoader().Load(); err == nil {
		t.Fatal("expected error for invalid duration")
	}
}

func TestLoader_LegacyFlatEnvName(t *testing.T) {
	// Nome sem separadores continua aceito como compatibilidade.
	t.Setenv("GOFAST_READHEADERTIMEOUT", "9s")

	cfg, err := NewLoader().Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ReadHeaderTimeout != 9*time.Second {
		t.Fatalf("ReadHeaderTimeout = %v, want 9s", cfg.ReadHeaderTimeout)
	}
}

func TestLoadFromEnvCaseSensitiveLikeLinux(t *testing.T) {
	// Regressão do CI: no Linux os.Getenv é case-sensitive. Em sistemas
	// case-insensitive (Windows) a busca por "GOFAST_Addr" encontra
	// "GOFAST_ADDR" e mascara o bug. Aqui lookupEnv é substituído por um mapa
	// case-sensitive, reproduzindo o comportamento do Linux.
	env := map[string]string{
		"GOFAST_ADDR":    ":9100", // nome canônico SNAKE_CASE: deve ser lido
		"GOFAST_Workers": "7",     // caixa mista: deve ser ignorado no Linux
	}

	orig := lookupEnv
	defer func() { lookupEnv = orig }()

	var queried []string
	lookupEnv = func(key string) string {
		queried = append(queried, key)
		return env[key]
	}

	cfg, fields, err := loadFromEnv("GOFAST_")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":9100" || !fields["Addr"] {
		t.Errorf("Addr = %q (definido=%v), want :9100", cfg.Addr, fields["Addr"])
	}
	if fields["Workers"] {
		t.Errorf("GOFAST_Workers não deve ser lido com semântica case-sensitive (workers=%d)", cfg.Workers)
	}
	for _, key := range queried {
		for _, r := range key {
			if r >= 'a' && r <= 'z' {
				t.Fatalf("consulta de env com minúsculas: %q", key)
			}
		}
	}
}
