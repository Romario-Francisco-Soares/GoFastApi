package gofast

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

// Status codes HTTP (equivalente ao fastapi.status).
const (
	StatusOK                  = 200
	StatusCreated             = 201
	StatusAccepted            = 202
	StatusNoContent           = 204
	StatusMovedPermanently    = 301
	StatusFound               = 302
	StatusSeeOther            = 303
	StatusNotModified         = 304
	StatusBadRequest          = 400
	StatusUnauthorized        = 401
	StatusForbidden           = 403
	StatusNotFound            = 404
	StatusMethodNotAllowed    = 405
	StatusConflict            = 409
	StatusUnprocessableEntity = 422
	StatusTooManyRequests     = 429
	StatusInternalServerError = 500
	StatusNotImplemented      = 501
)

// MediaType constants.
const (
	MediaTypeJSON   = "application/json"
	MediaTypeHTML   = "text/html"
	MediaTypeText   = "text/plain"
	MediaTypeXML    = "application/xml"
	MediaTypeStream = "text/event-stream"
)

// Response é a interface para respostas HTTP.
type Response interface {
	Render(http.ResponseWriter) error
	SetStatus(int)
	GetStatus() int
	SetHeader(string, string)
}

// BaseResponse implementa a interface Response.
//
// O mapa de headers é alocado de forma lazy (apenas quando SetHeader é
// chamado), evitando alocação para respostas sem headers customizados.
type BaseResponse struct {
	status      int
	headers     map[string]string // nil até primeiro SetHeader
	body        []byte
	contentType string
}

func NewBaseResponse(status int, contentType string, body []byte) *BaseResponse {
	return &BaseResponse{
		status:      status,
		body:        body,
		contentType: contentType,
	}
}

// ensureHeaders inicializa o mapa de headers sob demanda.
func (r *BaseResponse) ensureHeaders() {
	if r.headers == nil {
		r.headers = make(map[string]string)
	}
}

func (r *BaseResponse) Render(w http.ResponseWriter) error {
	for k, v := range r.headers {
		w.Header().Set(k, v)
	}
	if r.contentType != "" {
		w.Header().Set("Content-Type", r.contentType)
	}
	w.WriteHeader(r.status)
	if len(r.body) > 0 {
		_, err := w.Write(r.body)
		return err
	}
	return nil
}

func (r *BaseResponse) SetStatus(code int) { r.status = code }
func (r *BaseResponse) GetStatus() int     { return r.status }
func (r *BaseResponse) SetHeader(k, v string) {
	r.ensureHeaders()
	r.headers[k] = v
}

// JSONResponse retorna uma resposta JSON.
type JSONResponse struct {
	*BaseResponse
}

// JSONResponse cria uma resposta JSON a partir de dados.
func NewJSONResponse(data any, status int) JSONResponse {
	body, err := json.Marshal(data)
	if err != nil {
		body = []byte(`{"error":"failed to serialize response"}`)
		status = StatusInternalServerError
	}
	return JSONResponse{
		BaseResponse: NewBaseResponse(status, MediaTypeJSON, body),
	}
}

// FileResponse retorna um arquivo como resposta.
type FileResponse struct {
	*BaseResponse
	path string
	req  *http.Request
}

func NewFileResponse(path, mediaType, filename string, headers map[string]string, req *http.Request) *FileResponse {
	fr := &FileResponse{
		BaseResponse: NewBaseResponse(StatusOK, mediaType, nil),
		path:         path,
		req:          req,
	}
	for k, v := range headers {
		fr.SetHeader(k, v)
	}
	if filename != "" {
		fr.SetHeader("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
	}
	return fr
}

func (r *FileResponse) Render(w http.ResponseWriter) error {
	for k, v := range r.headers {
		w.Header().Set(k, v)
	}
	if r.contentType != "" {
		w.Header().Set("Content-Type", r.contentType)
	}
	req := r.req
	if req == nil {
		req, _ = http.NewRequest(http.MethodGet, "/", nil)
	}
	// ServeFile precisa controlar o status (404, 206, 304) e os cabeçalhos
	// condicionais/range; escrever o status antes quebraria esses recursos.
	if r.status == 0 || r.status == http.StatusOK {
		http.ServeFile(w, req, r.path)
		return nil
	}
	file, err := os.Open(r.path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	w.WriteHeader(r.status)
	_, err = io.Copy(w, file)
	_ = info
	return err
}

// TextResponse retorna texto simples.
type TextResponse struct {
	*BaseResponse
}

func NewTextResponse(content string, status int) TextResponse {
	return TextResponse{
		BaseResponse: NewBaseResponse(status, MediaTypeText, []byte(content)),
	}
}

// HTMLResponse retorna HTML.
type HTMLResponse struct {
	*BaseResponse
}

func NewHTMLResponse(content string) HTMLResponse {
	return HTMLResponse{
		BaseResponse: NewBaseResponse(StatusOK, MediaTypeHTML, []byte(content)),
	}
}

// RedirectResponse retorna um redirecionamento.
type RedirectResponse struct {
	*BaseResponse
}

func NewRedirectResponse(url string, status int) *RedirectResponse {
	if status == 0 {
		status = http.StatusFound
	}
	r := &RedirectResponse{BaseResponse: NewBaseResponse(status, "", nil)}
	r.SetHeader("Location", url)
	return r
}

func (r *RedirectResponse) Render(w http.ResponseWriter) error {
	w.Header().Set("Location", r.headers["Location"])
	if r.headers["Location"] == "" {
		http.Error(w, "redirect location missing", http.StatusInternalServerError)
		return nil
	}
	w.WriteHeader(r.status)
	return nil
}

// StreamResponse retorna um streaming de dados.
type StreamResponse struct {
	*BaseResponse
	stream func(io.Writer) error
}

func NewStreamResponse(stream func(io.Writer) error) *StreamResponse {
	return &StreamResponse{
		BaseResponse: NewBaseResponse(StatusOK, MediaTypeText, nil),
		stream:       stream,
	}
}

func (r *StreamResponse) Render(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", r.contentType)
	w.WriteHeader(r.status)
	return r.stream(w)
}

// EmptyResponse retorna uma resposta vazia.
type EmptyResponse struct {
	*BaseResponse
}

func NewEmptyResponse() EmptyResponse {
	return EmptyResponse{
		BaseResponse: NewBaseResponse(StatusNoContent, "", nil),
	}
}

// Created cria uma resposta 201 Created.
func Created(data any) JSONResponse {
	return NewJSONResponse(data, StatusCreated)
}

// OK cria uma resposta 200 OK.
func OK(data any) JSONResponse {
	return NewJSONResponse(data, StatusOK)
}

var _ Response = JSONResponse{}
var _ Response = (*FileResponse)(nil)
var _ Response = TextResponse{}
var _ Response = HTMLResponse{}
var _ Response = (*RedirectResponse)(nil)
var _ Response = (*StreamResponse)(nil)
var _ Response = EmptyResponse{}
