package testing

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	gofast "gofast"
)

// appTest cria uma aplicação com rotas úteis para testes do TestClient.
func appTest() *gofast.App {
	app := gofast.New()

	app.GET("/ping", func(ctx *gofast.Context) (any, error) {
		return gofast.OK(map[string]string{"status": "ok"}), nil
	})

	app.POST("/json", func(ctx *gofast.Context) (any, error) {
		var body map[string]any
		if err := ctx.JSON(&body); err != nil {
			return nil, gofast.NewHTTPError(http.StatusBadRequest, "bad_request", "invalid json")
		}
		return gofast.Created(map[string]any{"received": body}), nil
	})

	app.POST("/form", func(ctx *gofast.Context) (any, error) {
		return gofast.OK(map[string]string{"name": ctx.FormValue("name")}), nil
	})

	app.POST("/multipart", func(ctx *gofast.Context) (any, error) {
		name := ctx.FormValue("name")
		file, err := ctx.FormFile("file")
		if err != nil {
			return nil, gofast.NewHTTPError(http.StatusBadRequest, "bad_request", "missing file")
		}
		defer file.Close()
		data, _ := io.ReadAll(file)
		return gofast.OK(map[string]any{"name": name, "size": len(data)}), nil
	})

	app.GET("/cookie", func(ctx *gofast.Context) (any, error) {
		val, _ := ctx.Cookie("session")
		return gofast.OK(map[string]string{"session": val}), nil
	})

	app.POST("/cookie", func(ctx *gofast.Context) (any, error) {
		ctx.SetHeader("Set-Cookie", "session=abc123; Path=/; HttpOnly")
		return gofast.OK(map[string]string{"set": "true"}), nil
	})

	return app
}

func TestTestClient_Get(t *testing.T) {
	app := appTest()
	client := NewTestClient(app)
	defer client.Close()

	resp, err := client.Get("/ping")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	data, err := JSONResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	if data["status"] != "ok" {
		t.Fatalf("unexpected body: %v", data)
	}
}

func TestTestClient_PostJSON(t *testing.T) {
	app := appTest()
	client := NewTestClient(app)
	defer client.Close()

	body := []byte(`{"name":"Ada","age":31}`)
	resp, err := client.PostJSON("/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d, want 201", resp.StatusCode)
	}

	data, err := JSONResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	received, ok := data["received"].(map[string]any)
	if !ok {
		t.Fatalf("received is not a map: %v", data["received"])
	}
	if received["name"] != "Ada" {
		t.Fatalf("name mismatch: %v", received["name"])
	}
}

func TestTestClient_PostForm(t *testing.T) {
	app := appTest()
	client := NewTestClient(app)
	defer client.Close()

	data := url.Values{"name": {"Ada"}}
	resp, err := client.PostForm("/form", data)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	got, err := JSONResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	if got["name"] != "Ada" {
		t.Fatalf("name mismatch: %v", got["name"])
	}
}

func TestTestClient_PostMultipart(t *testing.T) {
	app := appTest()
	client := NewTestClient(app)
	defer client.Close()

	fields := map[string]string{"name": "report.pdf"}
	files := map[string][]byte{"file": []byte("conteudo do arquivo")}

	resp, err := client.PostMultipart("/multipart", fields, files)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	got, err := JSONResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	if got["name"] != "report.pdf" {
		t.Fatalf("name mismatch: %v", got["name"])
	}
	if got["size"] != float64(len("conteudo do arquivo")) {
		t.Fatalf("size mismatch: %v", got["size"])
	}
}

func TestTestClient_CookiePersistence(t *testing.T) {
	app := appTest()
	client := NewTestClient(app)
	defer client.Close()

	// Primeiro request define o cookie
	resp, err := client.client.Post(client.base+"/cookie", "application/x-www-form-urlencoded", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	// Segundo request deve enviar o cookie de volta
	resp, err = client.Get("/cookie")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}

	got, err := JSONResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	if got["session"] != "abc123" {
		t.Fatalf("cookie not persisted: %v", got["session"])
	}
}

func TestTestClient_NewRequestAndDo(t *testing.T) {
	app := appTest()
	client := NewTestClient(app)
	defer client.Close()

	req, err := client.NewRequest(http.MethodGet, "/ping", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
}

func TestTestClient_URL(t *testing.T) {
	app := appTest()
	client := NewTestClient(app)
	defer client.Close()

	if client.URL() == "" {
		t.Fatal("base URL não deve ser vazia")
	}
	if !strings.HasPrefix(client.URL(), "http://127.0.0.1:") {
		t.Fatalf("URL inesperada: %s", client.URL())
	}
}

func TestTestClient_Close(t *testing.T) {
	app := appTest()
	client := NewTestClient(app)

	// Depois de Close, requisições devem falhar
	client.Close()

	_, err := client.Get("/ping")
	if err == nil {
		t.Fatal("esperava erro após Close")
	}
}

func TestTestClient_InvalidJSON(t *testing.T) {
	app := appTest()
	client := NewTestClient(app)
	defer client.Close()

	resp, err := client.PostJSON("/json", []byte(`not json`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", resp.StatusCode)
	}
}

var _ = json.Unmarshal
