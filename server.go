package gofast

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// ServerConfig configura o servidor de produção (equivalente às configurações do Uvicorn).
type ServerConfig struct {
	// Binding
	Addr      string
	Host      string
	Port      int
	UDS       string
	FD        uintptr
	UseFD     bool
	Backlog   int
	ReusePort bool

	// TLS
	CertFile                 string
	KeyFile                  string
	ClientCAFile             string
	MinTLSVersion            string
	MaxTLSVersion            string
	TLSCipherSuites          []string
	PreferServerCipherSuites bool

	// HTTP/2
	HTTP2 bool
	H2C   bool

	// Development
	Reload        bool
	ReloadDirs    []string
	ReloadDelay   time.Duration
	ReloadPackage string // pacote main reconstruído no hot reload (vazio = autodetectar)

	// Workers
	Workers             int
	WorkerMode          string
	WorkerStartTimeout  time.Duration
	WorkerHealthTimeout time.Duration
	RestartPolicy       string
	MaxWorkerRestarts   int

	// HTTP
	ReadTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
	DisableKeepAlives bool

	// Limits
	MaxConnections    int
	ConcurrencyLimit  int
	MaxRequests       int
	MaxRequestsJitter int
	MaxBodyBytes      int64

	// Graceful
	GracefulTimeout      time.Duration
	ShutdownHooksTimeout time.Duration
	DrainTimeout         time.Duration

	// Proxy
	ProxyHeaders      bool
	TrustedProxies    []string
	TrustedHosts      []string
	ForwardedHeaders  []string
	ForwardedAllowIPs []string
	RootPath          string
	BasePath          string

	// Logging
	LogLevel        string
	AccessLog       bool
	AccessLogColors bool
	Logger          *slog.Logger
	AccessFormatter any
	ErrorLogger     *slog.Logger

	// Headers
	ServerHeader   string
	DateHeader     bool
	DefaultHeaders map[string]string

	// Config
	EnvPrefix  string
	ConfigFile string
	AppEnvFile string
	PIDFile    string

	// Signals
	HandleSignals       bool
	ShutdownSignals     []string
	ReloadSignal        string
	WorkerRestartSignal string

	// Systemd
	TimeoutNotify           time.Duration
	SystemdNotify           bool
	SystemdSocketActivation bool
}

// DefaultServerConfig retorna uma configuração de servidor com valores padrão.
func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		Addr:                    ":8000",
		Backlog:                 2048,
		ReadHeaderTimeout:       5 * time.Second,
		IdleTimeout:             75 * time.Second,
		GracefulTimeout:         30 * time.Second,
		MaxConnections:          10000,
		ConcurrencyLimit:        1000,
		MaxBodyBytes:            10 << 20,
		ProxyHeaders:            false,
		LogLevel:                "info",
		AccessLog:               true,
		Workers:                 1,
		ServerHeader:            "GoFastAPI",
		DateHeader:              true,
		HandleSignals:           true,
		TimeoutNotify:           5 * time.Second,
		SystemdNotify:           false,
		SystemdSocketActivation: false,
		ShutdownHooksTimeout:    10 * time.Second,
		DrainTimeout:            5 * time.Second,
	}
}

// ProductionServer é o servidor de produção integrado.
type ProductionServer struct {
	cfg      *ServerConfig
	app      *App
	server   *http.Server
	listener net.Listener
	running  bool
	mu       sync.RWMutex
	shutdown chan struct{}
}

// NewProductionServer cria um novo servidor de produção.
func NewProductionServer(app *App, cfg ServerConfig) *ProductionServer {
	return &ProductionServer{
		cfg:      &cfg,
		app:      app,
		shutdown: make(chan struct{}),
	}
}

// Start inicia o servidor com graceful shutdown e suporte a sinais.
func (s *ProductionServer) Start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("server already running")
	}

	// Validar HTTP/2 e h2C: são mutuamente exclusivos
	if s.cfg.HTTP2 && s.cfg.H2C {
		s.mu.Unlock()
		return fmt.Errorf("HTTP/2 (TLS) and h2C (cleartext) are mutually exclusive")
	}

	// Validar que HTTP/2 requer TLS (cert/key)
	if s.cfg.HTTP2 && (s.cfg.CertFile == "" || s.cfg.KeyFile == "") {
		s.mu.Unlock()
		return fmt.Errorf("HTTP/2 requires TLS (CertFile and KeyFile must be set)")
	}

	s.running = true
	s.mu.Unlock()

	var ln net.Listener
	var err error

	// Determinar endereço de escuta
	if s.cfg.UDS != "" {
		ln, err = net.Listen("unix", s.cfg.UDS)
	} else if s.cfg.Addr != "" {
		ln, err = net.Listen("tcp", s.cfg.Addr)
	} else if s.cfg.Host != "" && s.cfg.Port > 0 {
		ln, err = net.Listen("tcp", fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port))
	} else {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
		return fmt.Errorf("no listen address configured")
	}

	if err != nil {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
		return err
	}

	s.listener = ln

	// Configurar TLS se fornecido
	handler := s.app.Handler()
	var tlsConfig *tls.Config

	if s.cfg.CertFile != "" && s.cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(s.cfg.CertFile, s.cfg.KeyFile)
		if err != nil {
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
			return fmt.Errorf("failed to load TLS key pair: %w", err)
		}

		tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
		if s.cfg.HTTP2 {
			tlsConfig.NextProtos = []string{"h2", "http/1.1"}
		}

		// Configurar versão mínima TLS
		if s.cfg.MinTLSVersion != "" {
			switch s.cfg.MinTLSVersion {
			case "1.0":
				tlsConfig.MinVersion = tls.VersionTLS10
			case "1.1":
				tlsConfig.MinVersion = tls.VersionTLS11
			case "1.2":
				tlsConfig.MinVersion = tls.VersionTLS12
			case "1.3":
				tlsConfig.MinVersion = tls.VersionTLS13
			}
		}

		// Configurar versão máxima TLS
		if s.cfg.MaxTLSVersion != "" {
			switch s.cfg.MaxTLSVersion {
			case "1.2":
				tlsConfig.MaxVersion = tls.VersionTLS12
			case "1.3":
				tlsConfig.MaxVersion = tls.VersionTLS13
			}
		}

		// Configurar cipher suites se fornecidas
		if len(s.cfg.TLSCipherSuites) > 0 {
			var cipherSuites []uint16
			for _, cs := range s.cfg.TLSCipherSuites {
				switch cs {
				case "TLS_AES_128_GCM_SHA256":
					cipherSuites = append(cipherSuites, tls.TLS_AES_128_GCM_SHA256)
				case "TLS_AES_256_GCM_SHA384":
					cipherSuites = append(cipherSuites, tls.TLS_AES_256_GCM_SHA384)
				case "TLS_CHACHA20_POLY1305_SHA256":
					cipherSuites = append(cipherSuites, tls.TLS_CHACHA20_POLY1305_SHA256)
				case "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256":
					cipherSuites = append(cipherSuites, tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256)
				case "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256":
					cipherSuites = append(cipherSuites, tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256)
				case "TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384":
					cipherSuites = append(cipherSuites, tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384)
				case "TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384":
					cipherSuites = append(cipherSuites, tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384)
				}
			}
			if len(cipherSuites) > 0 {
				tlsConfig.CipherSuites = cipherSuites
				if s.cfg.PreferServerCipherSuites {
					tlsConfig.PreferServerCipherSuites = true
				}
			}
		}

		// Configurar Client CA se fornecida (mTLS)
		if s.cfg.ClientCAFile != "" {
			clientCA, err := os.ReadFile(s.cfg.ClientCAFile)
			if err != nil {
				s.mu.Lock()
				s.running = false
				s.mu.Unlock()
				return fmt.Errorf("failed to load client CA: %w", err)
			}
			clientCertPool := x509.NewCertPool()
			clientCertPool.AppendCertsFromPEM(clientCA)
			tlsConfig.ClientCAs = clientCertPool
			tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		}

		ln = tls.NewListener(ln, tlsConfig)
	}

	// Criar o servidor HTTP com configurações completas
	s.server = &http.Server{
		Handler:           handler,
		ReadTimeout:       s.cfg.ReadTimeout,
		ReadHeaderTimeout: s.cfg.ReadHeaderTimeout,
		WriteTimeout:      s.cfg.WriteTimeout,
		IdleTimeout:       s.cfg.IdleTimeout,
		MaxHeaderBytes:    s.cfg.MaxHeaderBytes,
	}

	// Configurar HTTP/2 se solicitado
	if s.cfg.H2C {
		handler = h2c.NewHandler(handler, &http2.Server{})
		s.server.Handler = handler
	}
	if s.cfg.HTTP2 || s.cfg.H2C {
		if err := http2.ConfigureServer(s.server, &http2.Server{}); err != nil {
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
			return fmt.Errorf("failed to configure HTTP/2 server: %w", err)
		}
	}

	s.running = true

	// Executar servidor em goroutine
	go func() {
		proto := "HTTP/1.1"
		if s.cfg.HTTP2 {
			proto = "HTTP/2 (TLS)"
		} else if s.cfg.H2C {
			proto = "h2c (cleartext)"
		}
		slog.Info("GoFastAPI server starting", "addr", s.cfg.Addr, "workers", s.cfg.Workers, "protocol", proto)
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("GoFastAPI server error", "error", err)
		}
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
		close(s.shutdown)
	}()

	return nil
}

// Stop para o servidor gracefulmente com drain.
func (s *ProductionServer) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.GracefulTimeout)
	defer cancel()

	return s.Shutdown(ctx)
}

// Shutdown realiza o desligamento graceful do servidor.
func (s *ProductionServer) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	s.mu.Unlock()

	// Executar hooks de shutdown do app
	appCtx, appCancel := context.WithTimeout(ctx, s.cfg.ShutdownHooksTimeout)
	defer appCancel()

	if err := s.app.Shutdown(appCtx); err != nil {
		slog.Warn("GoFastAPI app shutdown hooks error", "error", err)
	}

	// Fechar o servidor HTTP (para de aceitar novas conexões)
	if s.server != nil {
		if err := s.server.Shutdown(ctx); err != nil {
			slog.Warn("GoFastAPI server shutdown error", "error", err)
			return err
		}
	}

	s.mu.Lock()
	s.running = false
	s.mu.Unlock()

	close(s.shutdown)
	slog.Info("GoFastAPI server shut down gracefully")
	return nil
}

// Wait bloqueia até que o servidor seja desligado.
func (s *ProductionServer) Wait() {
	<-s.shutdown
}

// IsRunning retorna se o servidor está em execução.
func (s *ProductionServer) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// Addr retorna o endereço de escuta do servidor.
func (s *ProductionServer) Addr() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.cfg.Addr
}
