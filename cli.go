package gofast

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// CLI é a interface de linha de comando do GoFastAPI.
type CLI struct {
	app    *App
	args   []string
	logger *slog.Logger
}

// NewCLI cria uma nova instância da CLI.
func NewCLI(app *App, args []string) *CLI {
	return &CLI{
		app:    app,
		args:   args,
		logger: slog.Default(),
	}
}

// Run executa o comando CLI.
func (c *CLI) Run() int {
	if len(c.args) < 1 {
		c.printUsage()
		return 1
	}

	command := c.args[0]

	switch command {
	case "run":
		return c.runCmd(c.args[1:])
	case "dev":
		return c.devCmd(c.args[1:])
	case "openapi":
		return c.openapiCmd(c.args[1:])
	case "routes":
		return c.routesCmd(c.args[1:])
	case "version":
		return c.versionCmd()
	case "health":
		return c.healthCmd(c.args[1:])
	default:
		fmt.Printf("Comando desconhecido: %s\n", command)
		c.printUsage()
		return 1
	}
}

func (c *CLI) runCmd(args []string) int {
	addr := "127.0.0.1:8000"
	reload := false
	logLevel := "info"
	workers := 1

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--host":
			if i+1 < len(args) {
				addr = args[i+1] + ":8000"
				i++
			}
		case "--port":
			if i+1 < len(args) {
				if addr == "127.0.0.1:8000" {
					addr = "127.0.0.1:" + args[i+1]
				} else {
					parts := strings.Split(addr, ":")
					addr = parts[0] + ":" + args[i+1]
				}
				i++
			}
		case "--addr":
			if i+1 < len(args) {
				addr = args[i+1]
				i++
			}
		case "--reload":
			reload = true
		case "--log-level":
			if i+1 < len(args) {
				logLevel = args[i+1]
				i++
			}
		case "--workers":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &workers)
				i++
			}
		}
	}

	c.logger.Info("Iniciando servidor GoFastAPI em modo produção",
		"addr", addr, "reload", reload, "log_level", logLevel, "workers", workers)

	cfg := DefaultServerConfig()
	cfg.Addr = addr
	cfg.Reload = reload
	cfg.LogLevel = logLevel
	cfg.Workers = workers
	cfg.AccessLog = true
	cfg.HandleSignals = true

	if err := c.app.Run(cfg); err != nil {
		c.logger.Error("Erro ao iniciar servidor", "error", err)
		return 1
	}

	return 0
}

func (c *CLI) devCmd(args []string) int {
	addr := "127.0.0.1:8000"
	// Hot reload ainda não é suportado pelo runtime. O comando dev inicia
	// um único processo com logging de desenvolvimento, sem definir Reload.
	reload := false
	logLevel := "debug"

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--host":
			if i+1 < len(args) {
				addr = args[i+1] + ":8000"
				i++
			}
		case "--port":
			if i+1 < len(args) {
				if addr == "127.0.0.1:8000" {
					addr = "127.0.0.1:" + args[i+1]
				} else {
					parts := strings.Split(addr, ":")
					addr = parts[0] + ":" + args[i+1]
				}
				i++
			}
		case "--addr":
			if i+1 < len(args) {
				addr = args[i+1]
				i++
			}
		case "--no-reload":
			reload = false // mantido por compatibilidade com scripts existentes
		case "--log-level":
			if i+1 < len(args) {
				logLevel = args[i+1]
				i++
			}
		}
	}

	c.logger.Info("Iniciando servidor GoFastAPI em modo desenvolvimento",
		"addr", addr, "reload", reload, "log_level", logLevel,
		"hot_reload_supported", false)

	cfg := DefaultServerConfig()
	cfg.Addr = addr
	cfg.Reload = reload
	cfg.LogLevel = logLevel
	cfg.Workers = 1
	cfg.AccessLog = true
	cfg.AccessLogColors = true
	cfg.HandleSignals = true

	if err := c.app.Run(cfg); err != nil {
		c.logger.Error("Erro ao iniciar servidor", "error", err)
		return 1
	}

	return 0
}

func (c *CLI) openapiCmd(args []string) int {
	outFile := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--out" || arg == "-o" {
			if i+1 < len(args) {
				outFile = args[i+1]
				i++
			}
		}
	}

	doc := c.app.OpenAPI()

	if outFile != "" {
		if err := writeJSONFile(outFile, doc); err != nil {
			c.logger.Error("Erro ao salvar OpenAPI", "error", err)
			return 1
		}
		c.logger.Info("OpenAPI exportado", "file", outFile)
	} else {
		data, _ := json.Marshal(doc)
		fmt.Println(string(data))
	}

	return 0
}

func (c *CLI) routesCmd(args []string) int {
	c.app.router.mu.RLock()
	routes := append([]*Route(nil), c.app.router.allRoutes...)
	c.app.router.mu.RUnlock()
	if len(routes) == 0 {
		fmt.Println("Nenhuma rota registrada.")
		return 0
	}

	fmt.Printf("%-12s %-48s %s\n", "METHOD", "PATH", "OPERATION ID")
	fmt.Printf("%-12s %-48s %s\n", "------", "----", "------------")
	for _, route := range routes {
		name := route.OperationID
		if name == "" {
			name = route.Summary
		}
		fmt.Printf("%-12s %-48s %s\n", route.Method, route.Path, name)
	}
	return 0
}

func (c *CLI) versionCmd() int {
	fmt.Println("GoFastAPI v1.0.0")
	return 0
}

func (c *CLI) healthCmd(args []string) int {
	fmt.Println("Verificando saúde da aplicação...")
	health := c.app.Health()
	if !health.IsLive() {
		fmt.Println("Status: NOT LIVE")
		return 1
	}
	if !health.IsReady() {
		fmt.Println("Status: NOT READY")
		return 1
	}
	checks := health.Check()
	var failed bool
	for name, err := range checks {
		if err != nil {
			failed = true
			fmt.Printf("%s: unhealthy: %v\n", name, err)
		} else {
			fmt.Printf("%s: healthy\n", name)
		}
	}
	if failed {
		fmt.Println("Status: DEGRADED")
		return 1
	}
	fmt.Println("Status: OK")
	return 0
}

func (c *CLI) printUsage() {
	fmt.Println(`Uso: gofast <comando> [argumentos]

Comandos:
  run       Iniciar servidor em modo produção
  dev       Iniciar servidor em modo desenvolvimento (debug; sem hot reload)
  openapi   Gerar contrato OpenAPI
  routes    Listar rotas registradas
  version   Mostrar versão
  health    Verificar saúde da aplicação

Exemplos:
  app.RunCLI([]string{"run", "--addr", ":8000", "--workers", "4"})
  app.RunCLI([]string{"dev", "--addr", "127.0.0.1:8000"})
  app.RunCLI([]string{"openapi", "--out", "openapi.json"})
  app.RunCLI([]string{"routes"})
  app.RunCLI([]string{"version"})`)
}

// writeJSONFile escreve dados JSON em um arquivo.
func writeJSONFile(path string, data any) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return os.WriteFile(path, jsonData, 0644)
}

// RunCLI é uma função de conveniência para executar a CLI.
func RunCLI(app *App, args []string) int {
	cli := NewCLI(app, args)
	return cli.Run()
}
