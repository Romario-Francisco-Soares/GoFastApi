// Package config fornece carregamento de configuração para o GoFastAPI
// com precedência explícita: CLI > variáveis de ambiente > arquivo > defaults.
//
// Variáveis de ambiente usam o prefixo GOFAST_ (ex: GOFAST_ADDR, GOFAST_LOG_LEVEL).
// Arquivos de configuração usam JSON por padrão (sem dependências externas).
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"

	gofast "gofast"
)

// LoaderOption configura o ConfigLoader.
type LoaderOption func(*ConfigLoader)

// ConfigLoader carrega ServerConfig a partir de defaults, arquivo, env e CLI.
type ConfigLoader struct {
	envPrefix    string
	configFile   string
	cliOverrides map[string]any // chave -> valor (ex: {"Addr": ":9000", "Workers": 4})
}

// NewLoader cria um ConfigLoader com as opções informadas.
func NewLoader(opts ...LoaderOption) *ConfigLoader {
	c := &ConfigLoader{
		envPrefix:    "GOFAST_",
		configFile:   "",
		cliOverrides: make(map[string]any),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// WithEnvPrefix define o prefixo das variáveis de ambiente (padrão: GOFAST_).
func WithEnvPrefix(prefix string) LoaderOption {
	return func(c *ConfigLoader) {
		c.envPrefix = prefix
	}
}

// WithConfigFile define o caminho do arquivo de configuração (JSON).
func WithConfigFile(path string) LoaderOption {
	return func(c *ConfigLoader) {
		c.configFile = path
	}
}

// WithCLIOverrides define overrides vindos da linha de comando.
func WithCLIOverrides(overrides map[string]any) LoaderOption {
	return func(c *ConfigLoader) {
		for k, v := range overrides {
			c.cliOverrides[k] = v
		}
	}
}

// Load carrega a configuração seguindo a precedência:
// defaults > arquivo > env > CLI.
func (c *ConfigLoader) Load() (*gofast.ServerConfig, error) {
	// 1. Defaults
	defaults := gofast.DefaultServerConfig()
	cfg := &defaults

	// 2. Arquivo de configuração (JSON)
	// Copia apenas campos presentes no JSON (campos explicitamente definidos).
	if c.configFile != "" {
		fileCfg, fileFields, err := loadConfigFile(c.configFile)
		if err != nil {
			return nil, fmt.Errorf("carregar arquivo de configuração %q: %w", c.configFile, err)
		}
		cfg = mergeSelected(cfg, fileCfg, fileFields)
	}

	// 3. Variáveis de ambiente
	// Copia apenas campos com env var definida.
	envCfg, envFields, err := loadFromEnv(c.envPrefix)
	if err != nil {
		return nil, fmt.Errorf("carregar variáveis de ambiente: %w", err)
	}
	cfg = mergeSelected(cfg, envCfg, envFields)

	// 4. Overrides da CLI (última prioridade)
	if len(c.cliOverrides) > 0 {
		cfg = applyCLIOverrides(cfg, c.cliOverrides)
	}

	return cfg, nil
}

// loadConfigFile lê um arquivo JSON de configuração e retorna a configuração
// junto com o conjunto de campos presentes no arquivo.
func loadConfigFile(path string) (*gofast.ServerConfig, map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}

	// Primeiro faz parse em um mapa para saber quais campos estão presentes
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, fmt.Errorf("fazer parse do JSON: %w", err)
	}

	// Conjunto de campos presentes no JSON
	fields := make(map[string]bool)
	for k := range raw {
		fields[k] = true
	}

	// Depois faz parse na struct
	var cfg gofast.ServerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, nil, fmt.Errorf("fazer parse do JSON na struct: %w", err)
	}

	return &cfg, fields, nil
}

// loadFromEnv lê variáveis de ambiente com o prefixo informado.
// Retorna a configuração junto com o conjunto de campos que tinham env var definida.
func loadFromEnv(prefix string) (*gofast.ServerConfig, map[string]bool, error) {
	cfg := &gofast.ServerConfig{}
	fields := make(map[string]bool)

	// Percorre todos os campos de ServerConfig via reflection
	v := reflect.ValueOf(cfg).Elem()
	t := v.Type()

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		envName := prefix + field.Name
		val := os.Getenv(envName)
		if val == "" {
			continue
		}

		if err := setField(v.Field(i), field.Name, val); err != nil {
			return nil, nil, fmt.Errorf("variável %s: %w", envName, err)
		}
		fields[field.Name] = true
	}

	return cfg, fields, nil
}

// setField define o valor de um campo struct a partir de uma string de env.
func setField(v reflect.Value, fieldName string, val string) error {
	if !v.CanSet() {
		return nil
	}

	switch v.Kind() {
	case reflect.String:
		v.SetString(val)
	case reflect.Int, reflect.Int64:
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return fmt.Errorf("esperado inteiro, obtido %q: %w", val, err)
		}
		v.SetInt(n)
	case reflect.Bool:
		b, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("esperado booleano, obtido %q: %w", val, err)
		}
		v.SetBool(b)
	case reflect.Float64:
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return fmt.Errorf("esperado float, obtido %q: %w", val, err)
		}
		v.SetFloat(f)
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.String {
			parts := strings.Split(val, ",")
			cleaned := make([]string, 0, len(parts))
			for _, p := range parts {
				if p = strings.TrimSpace(p); p != "" {
					cleaned = append(cleaned, p)
				}
			}
			v.Set(reflect.ValueOf(cleaned))
		} else {
			return fmt.Errorf("slice de %s não suportado via env", v.Type().Elem().Kind())
		}
	case reflect.Uintptr:
		// FD (file descriptor) - ignorar via env (requer manipulação especial)
		return nil
	default:
		return fmt.Errorf("tipo %s não suportado para env", v.Kind())
	}
	return nil
}

// mergeSelected copia apenas os campos listados em 'fields' do override para o base.
// Isso permite precedência precisa: defaults < arquivo < env < CLI.
func mergeSelected(base, override *gofast.ServerConfig, fields map[string]bool) *gofast.ServerConfig {
	if base == nil {
		return override
	}
	if override == nil || len(fields) == 0 {
		return base
	}

	v := reflect.ValueOf(override).Elem()
	t := v.Type()

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !fields[field.Name] {
			continue
		}
		overrideField := v.Field(i)
		baseField := reflect.ValueOf(base).Elem().Field(i)
		if baseField.CanSet() {
			baseField.Set(overrideField)
		}
	}
	return base
}

// applyCLIOverrides aplica overrides da CLI sobre a configuração.
func applyCLIOverrides(cfg *gofast.ServerConfig, overrides map[string]any) *gofast.ServerConfig {
	v := reflect.ValueOf(cfg).Elem()
	t := v.Type()

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		val, ok := overrides[field.Name]
		if !ok {
			continue
		}

		fv := v.Field(i)
		if !fv.CanSet() {
			continue
		}

		rv := reflect.ValueOf(val)
		if !rv.Type().AssignableTo(fv.Type()) {
			// Tentar conversão simples
			switch fv.Kind() {
			case reflect.String:
				if s, ok := val.(string); ok {
					fv.SetString(s)
				}
			case reflect.Int, reflect.Int64:
				switch n := val.(type) {
				case int:
					fv.SetInt(int64(n))
				case int64:
					fv.SetInt(n)
				case float64:
					fv.SetInt(int64(n))
				}
			case reflect.Bool:
				if b, ok := val.(bool); ok {
					fv.SetBool(b)
				}
			case reflect.Slice:
				if fv.Type().Elem().Kind() == reflect.String {
					if sl, ok := val.([]string); ok {
						fv.Set(reflect.ValueOf(sl))
					}
				}
			}
			continue
		}
		fv.Set(rv)
	}

	return cfg
}

// LoadDefault carrega a configuração com defaults apenas (útil para testes).
func LoadDefault() *gofast.ServerConfig {
	cfg := gofast.DefaultServerConfig()
	return &cfg
}

// MustLoad é como Load, mas panic em caso de erro. Útil para inicialização de aplicação.
func MustLoad(opts ...LoaderOption) *gofast.ServerConfig {
	cfg, err := NewLoader(opts...).Load()
	if err != nil {
		panic(fmt.Errorf("config: %w", err))
	}
	return cfg
}
