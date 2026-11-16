package gofast

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"sync"
)

// Context é o wrapper de requisição do GoFastAPI, equivalente ao Request do FastAPI.
//
// params, session e state são inicializados de forma lazy (sob demanda) para
// evitar alocações desnecessárias em rotas que não os utilizam.
type Context struct {
	ctx             context.Context
	req             *http.Request
	w               http.ResponseWriter
	params          map[string]string // nil até primeiro uso (SetParam/Param)
	session         *Session          // nil até primeiro uso (Session())
	state           *State            // nil até primeiro uso (DI request-scope / State)
	backgroundTasks *BackgroundTasks
	di              *DIContainer
	cleanupMu       sync.Mutex
	cleanups        []func() error
	diCacheMu       sync.Mutex
	diCache         map[reflect.Type]any
}

// NewContext cria um novo contexto de requisição.
// Estruturas internas são alocadas sob demanda (zero alocação para rotas simples).
func NewContext(ctx context.Context, req *http.Request, w http.ResponseWriter) *Context {
	return &Context{
		ctx: ctx,
		req: req,
		w:   w,
	}
}

// ensureParams inicializa params lazily.
func (c *Context) ensureParams() {
	if c.params == nil {
		c.params = make(map[string]string)
	}
}

// ensureState inicializa state lazily.
func (c *Context) ensureState() {
	if c.state == nil {
		c.state = NewState()
	}
}

// ensureSession inicializa session lazily.
func (c *Context) ensureSession() {
	if c.session != nil {
		return
	}
	if s, ok := c.req.Context().Value(sessionContextKey{}).(*Session); ok && s != nil {
		c.session = s
		return
	}
	c.session = NewSession(c.req, c.w)
}

// Request retorna o *http.Request subjacente.
func (c *Context) Request() *http.Request {
	return c.req
}

// ResponseWriter retorna o http.ResponseWriter.
func (c *Context) ResponseWriter() http.ResponseWriter {
	return c.w
}

// Context retorna o context.Context.
func (c *Context) Context() context.Context {
	return c.ctx
}

// Param retorna um parâmetro de caminho.
// Leitura de mapa nil é segura em Go (retorna zero value).
func (c *Context) Param(key string) string {
	if v, ok := c.params[key]; ok {
		return v
	}
	return ""
}

// SetParam define um parâmetro de caminho (inicializa params sob demanda).
func (c *Context) SetParam(key, value string) {
	c.ensureParams()
	c.params[key] = value
}

// Query retorna um parâmetro de query string.
func (c *Context) Query(key string) string {
	return c.req.URL.Query().Get(key)
}

// QueryArray retorna múltiplos valores de um parâmetro de query.
func (c *Context) QueryArray(key string) []string {
	return c.req.URL.Query()[key]
}

// Header retorna um cabeçalho HTTP.
func (c *Context) Header(key string) string {
	return c.req.Header.Get(key)
}

// Cookie retorna um cookie pelo nome.
func (c *Context) Cookie(name string) (string, bool) {
	cookie, err := c.req.Cookie(name)
	if err != nil {
		return "", false
	}
	return cookie.Value, true
}

// Session retorna a sessão da requisição (inicializada sob demanda).
func (c *Context) Session() *Session {
	c.ensureSession()
	return c.session
}

// BackgroundTasks retorna a fila de tarefas desta requisição.
func (c *Context) BackgroundTasks() *BackgroundTasks {
	if c.backgroundTasks == nil {
		c.backgroundTasks = &BackgroundTasks{}
	}
	return c.backgroundTasks
}

// State retorna o estado lazy da requisição.
func (c *Context) State() *State {
	c.ensureState()
	return c.state
}

// Resolve resolve uma dependência registrada na aplicação associada ao Context.
func (c *Context) Resolve(t reflect.Type) (any, error) {
	if c.di == nil {
		return nil, fmt.Errorf("request Context is not attached to an application DI container")
	}
	return c.di.ResolveE(t, c)
}

func (c *Context) hasBackgroundTasks() bool {
	if c.backgroundTasks == nil {
		return false
	}
	c.backgroundTasks.mu.Lock()
	defer c.backgroundTasks.mu.Unlock()
	return len(c.backgroundTasks.tasks) > 0
}

func (c *Context) runBackgroundTasks() {
	c.backgroundTasks.run(c.ctx, nil)
}

func (c *Context) getDICache(t reflect.Type) (any, bool) {
	c.diCacheMu.Lock()
	defer c.diCacheMu.Unlock()
	value, ok := c.diCache[t]
	return value, ok
}

func (c *Context) setDICache(t reflect.Type, value any) {
	c.diCacheMu.Lock()
	if c.diCache == nil {
		c.diCache = make(map[reflect.Type]any)
	}
	c.diCache[t] = value
	c.diCacheMu.Unlock()
}

func (c *Context) clearDICache() {
	c.diCacheMu.Lock()
	c.diCache = nil
	c.diCacheMu.Unlock()
}

func (c *Context) addCleanup(cleanup func() error) {
	if c == nil || cleanup == nil {
		return
	}
	c.cleanupMu.Lock()
	c.cleanups = append(c.cleanups, cleanup)
	c.cleanupMu.Unlock()
}

func (c *Context) runCleanups() {
	if c == nil {
		return
	}
	c.cleanupMu.Lock()
	cleanups := append([]func() error(nil), c.cleanups...)
	c.cleanups = nil
	c.cleanupMu.Unlock()
	for i := len(cleanups) - 1; i >= 0; i-- {
		if err := cleanups[i](); err != nil {
			slog.Warn("GoFastAPI request dependency cleanup failed", "error", err)
		}
	}
}

// JSON decodifica o corpo JSON da requisição para um alvo.
func (c *Context) JSON(v any) error {
	if c.req.Body == nil {
		return nil
	}
	defer c.req.Body.Close()
	return json.NewDecoder(c.req.Body).Decode(v)
}

// Body retorna o corpo da requisição como bytes.
func (c *Context) Body() ([]byte, error) {
	if c.req.Body == nil {
		return nil, nil
	}
	defer c.req.Body.Close()
	return io.ReadAll(c.req.Body)
}

// StringBody retorna o corpo como string.
func (c *Context) StringBody() (string, error) {
	b, err := c.Body()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// FormFile retorna um arquivo enviado via multipart/form-data.
func (c *Context) FormFile(key string) (*UploadFile, error) {
	file, header, err := c.req.FormFile(key)
	if err != nil {
		return nil, err
	}
	return &UploadFile{
		File:        file,
		Filename:    header.Filename,
		Size:        header.Size,
		Header:      http.Header(header.Header),
		ContentType: header.Header.Get("Content-Type"),
	}, nil
}

// FormValue retorna um valor de formulário.
func (c *Context) FormValue(key string) string {
	return c.req.FormValue(key)
}

// FormValues retorna múltiplos valores de um campo de formulário.
func (c *Context) FormValues(key string) []string {
	return c.req.Form[key]
}

// RemoteAddr retorna o endereço IP remoto.
func (c *Context) RemoteAddr() string {
	return c.req.RemoteAddr
}

// Method retorna o método HTTP.
func (c *Context) Method() string {
	return c.req.Method
}

// URL retorna a URL da requisição.
func (c *Context) URL() string {
	return c.req.URL.String()
}

// Path retorna o caminho da URL.
func (c *Context) Path() string {
	return c.req.URL.Path
}

// ContentType retorna o Content-Type da requisição.
func (c *Context) ContentType() string {
	return c.req.Header.Get("Content-Type")
}

// IsWebSocket verifica se a requisição é um upgrade de WebSocket.
func (c *Context) IsWebSocket() bool {
	return strings.Contains(c.req.Header.Get("Upgrade"), "websocket")
}

// SetHeader define um cabeçalho de resposta.
func (c *Context) SetHeader(key, value string) {
	c.w.Header().Set(key, value)
}

// SetStatus define o código de status da resposta.
func (c *Context) SetStatus(code int) {
	c.w.WriteHeader(code)
}

// Write escreve dados na resposta.
func (c *Context) Write(data []byte) (int, error) {
	return c.w.Write(data)
}

// WriteString escreve uma string na resposta.
func (c *Context) WriteString(s string) (int, error) {
	return c.w.Write([]byte(s))
}

// UploadFile representa um arquivo enviado via multipart.
type UploadFile struct {
	File        io.ReadCloser
	Filename    string
	Size        int64
	Header      http.Header
	ContentType string
}

// Close fecha o arquivo.
func (f *UploadFile) Close() error {
	if f.File != nil {
		return f.File.Close()
	}
	return nil
}

// Read lê dados do arquivo.
func (f *UploadFile) Read(p []byte) (int, error) {
	if f.File == nil {
		return 0, io.EOF
	}
	return f.File.Read(p)
}

// Session gerencia sessão de usuário via cookie.
type Session struct {
	mu    sync.RWMutex
	req   *http.Request
	w     http.ResponseWriter
	data  map[string]any
	dirty bool
	cfg   SessionConfig
	key   []byte
}

// NewSession cria uma nova sessão.
func NewSession(req *http.Request, w http.ResponseWriter) *Session {
	return &Session{
		req:  req,
		w:    w,
		data: make(map[string]any),
	}
}

// Get obtém um valor da sessão.
func (s *Session) Get(key string) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	return v, ok
}

// GetString obtém um valor como string.
func (s *Session) GetString(key, def string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if v, ok := s.data[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return def
}

// Set define um valor na sessão.
func (s *Session) Set(key string, value any) {
	s.mu.Lock()
	s.data[key] = value
	s.dirty = true
	s.mu.Unlock()
	_ = s.Save()
}

// Delete remove um valor da sessão.
func (s *Session) Delete(key string) {
	s.mu.Lock()
	delete(s.data, key)
	s.dirty = true
	s.mu.Unlock()
	_ = s.Save()
}

// Save salva a sessão (deve ser chamado antes de escrever a resposta).
func (s *Session) Save() error {
	return s.saveCookie()
}

// SaveAfterWrite agenda o salvamento após a resposta ser escrita.
func (c *Context) SaveSession() error {
	c.ensureSession()
	return c.session.Save()
}
