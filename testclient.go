package gofast

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
)

// TestClient é um cliente HTTP de integração sobre httptest.Server. Ele usa
// uma pilha HTTP real em loopback e mantém cookies entre requisições.
type TestClient struct {
	server *httptest.Server
	client *http.Client
}

// NewTestClient inicia um servidor HTTP de teste para a aplicação.
func NewTestClient(app *App) *TestClient {
	jar, _ := cookiejar.New(nil)
	server := httptest.NewServer(app.Handler())
	return &TestClient{server: server, client: &http.Client{Jar: jar}}
}

// URL retorna a URL base do servidor de teste.
func (c *TestClient) URL() string { return c.server.URL }

// Client retorna o http.Client subjacente para cenários avançados.
func (c *TestClient) Client() *http.Client { return c.client }

// Close encerra o servidor de teste.
func (c *TestClient) Close() {
	if c != nil && c.server != nil {
		c.server.Close()
	}
}

// NewRequest cria uma requisição associada à URL base do TestClient.
func (c *TestClient) NewRequest(method, path string, body io.Reader) (*http.Request, error) {
	if path == "" {
		path = "/"
	}
	if path[0] != '/' {
		path = "/" + path
	}
	return http.NewRequest(method, c.server.URL+path, body)
}

// Do executa uma requisição e retorna a resposta HTTP.
func (c *TestClient) Do(req *http.Request) (*http.Response, error) { return c.client.Do(req) }

// Get executa uma requisição GET.
func (c *TestClient) Get(path string) (*http.Response, error) {
	req, err := c.NewRequest(http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

// PostJSON executa uma requisição POST com corpo JSON.
func (c *TestClient) PostJSON(path string, body []byte) (*http.Response, error) {
	req, err := c.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.Do(req)
}

// PostForm executa uma requisição application/x-www-form-urlencoded.
func (c *TestClient) PostForm(path string, values url.Values) (*http.Response, error) {
	req, err := c.NewRequest(http.MethodPost, path, bytes.NewBufferString(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.Do(req)
}

// PostMultipart envia campos e arquivos multipart. O map de arquivos associa
// o nome do campo ao nome do arquivo e ao conteúdo.
func (c *TestClient) PostMultipart(path string, fields map[string]string, files map[string]TestFile) (*http.Response, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return nil, err
		}
	}
	for field, file := range files {
		part, err := writer.CreateFormFile(field, file.Filename)
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(file.Content); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	req, err := c.NewRequest(http.MethodPost, path, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return c.Do(req)
}

// TestFile descreve um arquivo enviado por PostMultipart.
type TestFile struct {
	Filename string
	Content  []byte
}
