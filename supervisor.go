package gofast

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// WorkerConfig configura um worker individual.
type WorkerConfig struct {
	ID                int
	AppPath           string // Caminho para o executável da aplicação
	Args              []string
	Env               []string
	MaxRequests       int
	MaxRequestsJitter int
	HealthTimeout     time.Duration
	StartTimeout      time.Duration
}

// WorkerStatus representa o status de um worker.
type WorkerStatus int

const (
	WorkerStatusStarting WorkerStatus = iota
	WorkerStatusHealthy
	WorkerStatusUnhealthy
	WorkerStatusStopping
	WorkerStatusStopped
	WorkerStatusCrashed
)

// WorkerInfo contém informações sobre um worker.
type WorkerInfo struct {
	ID            int
	PID           int
	Status        WorkerStatus
	StartTime     time.Time
	LastHeartbeat time.Time
	Requests      int
	Restarts      int
	ExitCode      int
	Error         error
}

// Supervisor gerencia múltiplos workers (processos filhos).
type Supervisor struct {
	cfg       *SupervisorConfig
	workers   map[int]*Worker
	workersMu sync.RWMutex
	listener  net.Listener
	running   bool
	mu        sync.RWMutex
	pidFile   string
	logger    *slog.Logger
	shutdown  chan struct{}
}

// SupervisorConfig configura o supervisor.
type SupervisorConfig struct {
	Workers                 int
	WorkerMode              string // "process" ou "goroutine"
	WorkerStartTimeout      time.Duration
	WorkerHealthTimeout     time.Duration
	RestartPolicy           string // "always", "on-failure", "no"
	MaxWorkerRestarts       int
	MaxRequests             int
	MaxRequestsJitter       int
	PIDFile                 string
	SystemdNotify           bool
	SystemdSocketActivation bool
	HandleSignals           bool
	ShutdownTimeout         time.Duration
	Logger                  *slog.Logger
}

// Worker representa um processo filho gerenciado.
type Worker struct {
	id            int
	cfg           *WorkerConfig
	cmd           *exec.Cmd
	process       *os.Process
	status        WorkerStatus
	mu            sync.RWMutex
	startTime     time.Time
	lastHB        time.Time
	requests      int
	restarts      int
	stopRequested bool // interrupção intencional, não deve disparar restart automático
	exitCode      int
	err           error
	done          chan struct{}
}

// NewSupervisor cria um novo supervisor.
func NewSupervisor(cfg SupervisorConfig) *Supervisor {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 1
	}
	if cfg.WorkerStartTimeout == 0 {
		cfg.WorkerStartTimeout = 10 * time.Second
	}
	if cfg.WorkerHealthTimeout == 0 {
		cfg.WorkerHealthTimeout = 30 * time.Second
	}
	if cfg.MaxWorkerRestarts <= 0 {
		cfg.MaxWorkerRestarts = 10
	}
	if cfg.ShutdownTimeout == 0 {
		cfg.ShutdownTimeout = 30 * time.Second
	}

	return &Supervisor{
		cfg:      &cfg,
		workers:  make(map[int]*Worker),
		running:  false,
		shutdown: make(chan struct{}),
		logger:   cfg.Logger,
	}
}

// Start inicia o supervisor e os workers.
func (s *Supervisor) Start(appPath string, args []string, listener net.Listener) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("supervisor already running")
	}
	s.running = true
	s.mu.Unlock()

	s.listener = listener

	// Gravar PID file se configurado
	if s.cfg.PIDFile != "" {
		if err := s.writePIDFile(); err != nil {
			s.logger.Warn("failed to write PID file", "error", err)
		}
	}

	// Iniciar workers e exigir pelo menos um worker pronto antes de declarar
	// o supervisor saudável.
	started := 0
	var startErrors []error
	for i := 0; i < s.cfg.Workers; i++ {
		if err := s.startWorker(i, appPath, args); err != nil {
			s.logger.Error("failed to start worker", "worker", i, "error", err)
			startErrors = append(startErrors, err)
		} else {
			started++
		}
	}
	if started == 0 {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
		s.removePIDFile()
		close(s.shutdown)
		return fmt.Errorf("no worker became ready: %v", startErrors)
	}
	if s.cfg.SystemdNotify {
		s.notifySystemd("READY=1")
	}

	// Iniciar loop de monitoramento
	go s.monitorLoop()

	// Configurar handlers de sinal
	if s.cfg.HandleSignals {
		s.setupSignalHandler()
	}

	s.logger.Info("supervisor started", "workers", s.cfg.Workers)
	return nil
}

// startWorker inicia um worker individual.
func (s *Supervisor) startWorker(id int, appPath string, args []string) error {
	previousRestarts := 0
	s.workersMu.RLock()
	if previous := s.workers[id]; previous != nil {
		previous.mu.RLock()
		previousRestarts = previous.restarts
		previous.mu.RUnlock()
	}
	s.workersMu.RUnlock()
	w := &Worker{
		id: id,
		cfg: &WorkerConfig{
			ID: id, AppPath: appPath, Args: args,
			MaxRequests: s.cfg.MaxRequests, MaxRequestsJitter: s.cfg.MaxRequestsJitter,
			HealthTimeout: s.cfg.WorkerHealthTimeout, StartTimeout: s.cfg.WorkerStartTimeout,
		},
		status: WorkerStatusStarting, startTime: time.Now(), restarts: previousRestarts, done: make(chan struct{}),
	}

	workerArgs := append([]string{"-worker", fmt.Sprintf("--id=%d", id)}, args...)
	cmd := exec.Command(appPath, workerArgs...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(),
		"GOFAST_WORKER=1",
		fmt.Sprintf("GOFAST_WORKER_ID=%d", id),
		fmt.Sprintf("GOFAST_MAX_REQUESTS=%d", s.cfg.MaxRequests),
		fmt.Sprintf("GOFAST_MAX_REQUESTS_JITTER=%d", s.cfg.MaxRequestsJitter),
	)

	var listenerFile *os.File
	if s.listener != nil {
		switch listener := s.listener.(type) {
		case *net.TCPListener:
			file, err := listener.File()
			if err != nil {
				return fmt.Errorf("duplicate TCP listener for worker %d: %w", id, err)
			}
			listenerFile = file
		case *net.UnixListener:
			file, err := listener.File()
			if err != nil {
				return fmt.Errorf("duplicate Unix listener for worker %d: %w", id, err)
			}
			listenerFile = file
		default:
			return fmt.Errorf("worker mode does not support listener type %T", s.listener)
		}
	}
	if listenerFile == nil {
		return fmt.Errorf("worker mode requires a TCP or Unix listener")
	}

	readyReader, readyWriter, err := os.Pipe()
	if err != nil {
		_ = listenerFile.Close()
		return fmt.Errorf("create worker readiness pipe: %w", err)
	}
	cmd.ExtraFiles = []*os.File{listenerFile, readyWriter} // fd 3 = listener; fd 4 = readiness
	cmd.Env = append(cmd.Env, "GOFAST_LISTENER_FD=3", "GOFAST_READY_FD=4")

	if err := cmd.Start(); err != nil {
		_ = listenerFile.Close()
		_ = readyReader.Close()
		_ = readyWriter.Close()
		w.mu.Lock()
		w.status, w.err = WorkerStatusCrashed, err
		w.mu.Unlock()
		close(w.done)
		return fmt.Errorf("failed to start worker %d: %w", id, err)
	}
	_ = listenerFile.Close()
	_ = readyWriter.Close()
	w.process = cmd.Process

	// Monitorar imediatamente para capturar processos que terminem durante
	// o handshake de prontidão.
	go s.monitorWorker(w, cmd)

	readyResult := make(chan error, 1)
	go func() {
		line, readErr := bufio.NewReader(readyReader).ReadString('\n')
		if readErr == nil && line != "READY\n" {
			readErr = fmt.Errorf("unexpected worker readiness message %q", strings.TrimSpace(line))
		}
		readyResult <- readErr
	}()
	select {
	case readyErr := <-readyResult:
		_ = readyReader.Close()
		if readyErr != nil {
			_ = cmd.Process.Kill()
			return fmt.Errorf("worker %d did not become ready: %w", id, readyErr)
		}
	case <-time.After(w.cfg.StartTimeout):
		_ = readyReader.Close()
		_ = cmd.Process.Kill()
		return fmt.Errorf("worker %d readiness timeout after %s", id, w.cfg.StartTimeout)
	}

	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		return fmt.Errorf("worker %d exited before readiness was committed: %w", id, err)
	}
	w.mu.Lock()
	if w.status == WorkerStatusCrashed || w.status == WorkerStatusStopped {
		w.mu.Unlock()
		return fmt.Errorf("worker %d exited during startup", id)
	}
	w.status = WorkerStatusHealthy
	w.lastHB = time.Now() // timestamp de prontidão; não é heartbeat periódico
	w.mu.Unlock()
	s.workersMu.Lock()
	s.workers[id] = w
	s.workersMu.Unlock()
	s.logger.Info("worker ready", "worker", id, "pid", cmd.Process.Pid)
	return nil
}

// monitorWorker monitora um worker individual.
func (s *Supervisor) monitorWorker(w *Worker, cmd *exec.Cmd) {
	defer close(w.done)
	err := cmd.Wait()
	w.mu.Lock()
	if cmd.ProcessState != nil {
		w.exitCode = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		w.err = err
		w.status = WorkerStatusCrashed
	} else {
		w.status = WorkerStatusStopped
	}
	w.mu.Unlock()

	s.logger.Info("worker exited", "worker", w.id, "pid", cmd.Process.Pid, "exit_code", w.exitCode, "error", err)
	s.mu.RLock()
	running := s.running
	policy := strings.ToLower(strings.TrimSpace(s.cfg.RestartPolicy))
	s.mu.RUnlock()
	w.mu.RLock()
	intentionalStop := w.stopRequested
	w.mu.RUnlock()
	shouldRestart := policy == "always" || (policy != "no" && err != nil)
	if policy == "" {
		shouldRestart = err != nil
	}
	if running && shouldRestart && !intentionalStop {
		s.scheduleRestart(w)
	}
}

// scheduleRestart agenda um restart do worker com backoff.
func (s *Supervisor) scheduleRestart(w *Worker) {
	w.mu.RLock()
	restarts := w.restarts
	w.mu.RUnlock()

	if restarts >= s.cfg.MaxWorkerRestarts {
		s.logger.Error("worker max restarts reached", "worker", w.id, "restarts", restarts)
		return
	}

	// Backoff exponencial simples
	backoff := time.Duration(restarts+1) * time.Second
	if backoff > 30*time.Second {
		backoff = 30 * time.Second
	}

	s.logger.Info("scheduling worker restart", "worker", w.id, "backoff", backoff)

	time.AfterFunc(backoff, func() {
		s.mu.RLock()
		running := s.running
		s.mu.RUnlock()

		if !running {
			return
		}

		w.mu.Lock()
		w.restarts++
		w.status = WorkerStatusStarting
		w.mu.Unlock()

		appPath := w.cfg.AppPath
		args := w.cfg.Args

		if err := s.startWorker(w.id, appPath, args); err != nil {
			s.logger.Error("failed to restart worker", "worker", w.id, "error", err)
		}
	})
}

// monitorLoop monitora todos os workers e faz health check.
func (s *Supervisor) monitorLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.shutdown:
			return
		case <-ticker.C:
			s.checkWorkersHealth()
		}
	}
}

// checkWorkersHealth verifica a saúde de todos os workers.
func (s *Supervisor) checkWorkersHealth() {
	s.workersMu.RLock()
	workers := make([]*Worker, 0, len(s.workers))
	for _, w := range s.workers {
		workers = append(workers, w)
	}
	s.workersMu.RUnlock()

	for _, w := range workers {
		w.mu.RLock()
		status, process := w.status, w.process
		w.mu.RUnlock()
		if status == WorkerStatusCrashed || status == WorkerStatusStopped || process == nil {
			continue
		}
		// Sem canal de heartbeat implementado, verificar apenas a existência do
		// processo. Não reiniciar workers saudáveis por timestamp nunca atualizado.
		if err := process.Signal(syscall.Signal(0)); err != nil {
			w.mu.Lock()
			w.status = WorkerStatusUnhealthy
			w.mu.Unlock()
			s.logger.Warn("worker process is not alive", "worker", w.id, "pid", process.Pid, "error", err)
		}
	}
}

// restartWorker reinicia um worker específico.
func (s *Supervisor) restartWorker(w *Worker) {
	s.mu.RLock()
	running := s.running
	s.mu.RUnlock()

	if !running {
		return
	}

	pid := 0
	if w.process != nil {
		pid = w.process.Pid
	}
	s.logger.Info("restarting worker", "worker", w.id, "pid", pid)

	// Parar intencionalmente o worker atual; monitorWorker não deve agendar
	// um segundo restart em paralelo com este fluxo explícito.
	w.mu.Lock()
	w.stopRequested = true
	w.status = WorkerStatusStopping
	w.mu.Unlock()
	if w.process != nil {
		_ = w.process.Signal(syscall.SIGTERM)
		// Aguardar um pouco para encerrar graciosamente
		select {
		case <-w.done:
		case <-time.After(5 * time.Second):
			w.process.Kill()
		}
	}

	// Iniciar novo worker
	appPath := w.cfg.AppPath
	args := w.cfg.Args

	if err := s.startWorker(w.id, appPath, args); err != nil {
		s.logger.Error("failed to restart worker", "worker", w.id, "error", err)
	}
}

// rollingRestart reinicia workers um por vez (SIGHUP).
func (s *Supervisor) rollingRestart() {
	s.workersMu.RLock()
	workerIDs := make([]int, 0, len(s.workers))
	for id := range s.workers {
		workerIDs = append(workerIDs, id)
	}
	s.workersMu.RUnlock()

	// Ordenar IDs para reiniciar em sequência
	for _, id := range workerIDs {
		s.workersMu.RLock()
		w := s.workers[id]
		s.workersMu.RUnlock()

		if w == nil {
			continue
		}

		s.logger.Info("rolling restart worker", "worker", id)

		s.restartWorker(w)

		// Aguardar o worker ficar saudável antes de reiniciar o próximo
		time.Sleep(2 * time.Second)
	}
}

// writePIDFile grava o PID do supervisor no arquivo.
func (s *Supervisor) writePIDFile() error {
	if s.cfg.PIDFile == "" {
		return nil
	}

	dir := filepath.Dir(s.cfg.PIDFile)
	if dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	pid := strconv.Itoa(os.Getpid())
	return os.WriteFile(s.cfg.PIDFile, []byte(pid), 0644)
}

// removePIDFile remove o arquivo PID.
func (s *Supervisor) removePIDFile() {
	if s.cfg.PIDFile != "" {
		os.Remove(s.cfg.PIDFile)
	}
}

// notifySystemd envia notificação para o systemd.
func (s *Supervisor) notifySystemd(state string) {
	if !s.cfg.SystemdNotify {
		return
	}

	socket := os.Getenv("NOTIFY_SOCKET")
	if socket == "" {
		return
	}

	// Enviar notificação via socket Unix do systemd
	conn, err := net.Dial("unixgram", socket)
	if err != nil {
		s.logger.Warn("failed to connect to systemd notify socket", "error", err)
		return
	}
	defer conn.Close()

	msg := []byte(state)
	_, err = conn.Write(msg)
	if err != nil {
		s.logger.Warn("failed to send systemd notification", "error", err)
	}
}

// setupSignalHandler configura handlers para sinais do sistema.
func (s *Supervisor) setupSignalHandler() {
	sigChan := make(chan os.Signal, 1)

	// Capturar SIGHUP para rolling restart, SIGINT/SIGTERM para shutdown
	signal.Notify(sigChan, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		for sig := range sigChan {
			switch sig {
			case syscall.SIGHUP:
				s.logger.Info("received SIGHUP, starting rolling restart")
				s.rollingRestart()
			case syscall.SIGINT, syscall.SIGTERM:
				s.logger.Info("received shutdown signal", "signal", sig.String())
				s.Stop()
			}
		}
	}()
}

// Stop para todos os workers e o supervisor.
func (s *Supervisor) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	s.mu.Unlock()

	s.logger.Info("stopping supervisor")

	// Notificar systemd que está parando
	if s.cfg.SystemdNotify {
		s.notifySystemd("STOPPING=1")
	}

	// Parar todos os workers
	s.workersMu.RLock()
	workers := make([]*Worker, 0, len(s.workers))
	for _, w := range s.workers {
		workers = append(workers, w)
	}
	s.workersMu.RUnlock()

	for _, w := range workers {
		w.mu.Lock()
		w.stopRequested = true
		w.status = WorkerStatusStopping
		w.mu.Unlock()
		if w.process != nil {
			_ = w.process.Signal(syscall.SIGTERM)
		}
	}

	// Aguardar workers terminarem
	done := make(chan struct{})
	go func() {
		s.workersMu.RLock()
		for _, w := range s.workers {
			<-w.done
		}
		s.workersMu.RUnlock()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(s.cfg.ShutdownTimeout):
		s.logger.Warn("workers did not stop gracefully, forcing kill")
		s.workersMu.RLock()
		for _, w := range s.workers {
			if w.process != nil {
				w.process.Kill()
			}
		}
		s.workersMu.RUnlock()
	}

	close(s.shutdown)
	s.removePIDFile()
	if s.listener != nil {
		_ = s.listener.Close()
	}

	s.logger.Info("supervisor stopped")
	return nil
}

// GetWorkersInfo retorna informações sobre todos os workers.
func (s *Supervisor) GetWorkersInfo() []WorkerInfo {
	s.workersMu.RLock()
	defer s.workersMu.RUnlock()

	info := make([]WorkerInfo, 0, len(s.workers))
	for _, w := range s.workers {
		w.mu.RLock()
		pid := 0
		if w.process != nil {
			pid = w.process.Pid
		}
		info = append(info, WorkerInfo{
			ID:            w.id,
			PID:           pid,
			Status:        w.status,
			StartTime:     w.startTime,
			LastHeartbeat: w.lastHB,
			Requests:      w.requests,
			Restarts:      w.restarts,
			ExitCode:      w.exitCode,
			Error:         w.err,
		})
		w.mu.RUnlock()
	}

	return info
}

// IsRunning retorna se o supervisor está em execução.
func (s *Supervisor) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Wait bloqueia até que o supervisor seja parado.
func (s *Supervisor) Wait() {
	<-s.shutdown
}

// AtualizarHeartbeat atualiza o heartbeat de um worker (chamado pelo próprio worker).
func (s *Supervisor) AtualizarHeartbeat(workerID int) {
	s.workersMu.RLock()
	w, ok := s.workers[workerID]
	s.workersMu.RUnlock()

	if ok {
		w.mu.Lock()
		w.lastHB = time.Now()
		w.status = WorkerStatusHealthy
		w.mu.Unlock()
	}
}

// IncrementarRequests incrementa o contador de requests de um worker.
func (s *Supervisor) IncrementarRequests(workerID int) {
	s.workersMu.RLock()
	w, ok := s.workers[workerID]
	s.workersMu.RUnlock()

	if ok {
		w.mu.Lock()
		w.requests++
		// Verificar se atingiu MaxRequests para reciclagem
		if w.cfg.MaxRequests > 0 && w.requests >= w.cfg.MaxRequests {
			w.mu.Unlock()
			s.logger.Info("worker reached MaxRequests, scheduling restart", "worker", workerID, "requests", w.requests)
			go s.restartWorker(w)
			return
		}
		w.mu.Unlock()
	}
}
