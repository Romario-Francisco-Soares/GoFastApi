package hotreload

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Build compila o pacote main da aplicação em um binário temporário.
// Quando pkg é vazio, detecta automaticamente o primeiro pacote main do módulo
// corrente (via `go list ./...`).
func Build(pkg string) (string, error) {
	if pkg == "" {
		detected, err := DetectMainPackage()
		if err != nil {
			return "", err
		}
		pkg = detected
	}

	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	exe := filepath.Join(os.TempDir(), fmt.Sprintf("gofast-reload-%d%s", os.Getpid(), suffix))

	cmd := exec.Command("go", "build", "-o", exe, pkg)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go build %s: %w", pkg, err)
	}
	return exe, nil
}

// DetectMainPackage retorna o primeiro pacote main encontrado em ./... no
// módulo corrente. Projetos com múltiplos binários devem definir
// ServerConfig.ReloadPackage explicitamente.
func DetectMainPackage() (string, error) {
	out, err := exec.Command("go", "list", "-f", `{{if eq .Name "main"}}{{.ImportPath}}{{end}}`, "./...").Output()
	if err != nil {
		return "", fmt.Errorf("go list ./...: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if pkg := strings.TrimSpace(line); pkg != "" {
			return pkg, nil
		}
	}
	return "", fmt.Errorf("no main package found under ./...; set ServerConfig.ReloadPackage")
}

// Restart inicia exe com args como um novo processo, herdando stdin, stdout,
// stderr e o ambiente, e então encerra o processo atual. O servidor deve ser
// finalizado antes da chamada.
func Restart(exe string, args []string) error {
	cmd := exec.Command(exe, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start rebuilt process: %w", err)
	}
	os.Exit(0)
	return nil
}
