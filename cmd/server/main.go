package main

import (
	"fmt"
	"log"
	"strings"

	gofast "gofast"
)

type createItemPayload struct {
	Name string `json:"name"`
}

func (p *createItemPayload) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("name is required")
	}
	return nil
}

func main() {
	app := gofast.New(
		gofast.Title("GoFastAPI Example"),
		gofast.Version("1.0.0"),
		gofast.Description("Aplicação de exemplo do GoFastAPI"),
	)

	app.GET("/", func(ctx *gofast.Context) (any, error) {
		return gofast.OK(map[string]string{"message": "Hello from GoFastAPI"}), nil
	})

	app.GET("/items/{item_id}", func(ctx *gofast.Context) (any, error) {
		return gofast.OK(map[string]string{
			"item_id": ctx.Param("item_id"),
			"q":       ctx.Query("q"),
		}), nil
	})

	app.POST("/items", func(ctx *gofast.Context, input createItemPayload) (any, error) {
		return gofast.Created(map[string]string{"name": input.Name}), nil
	})

	cfg := gofast.DefaultServerConfig()
	cfg.Addr = ":8000"
	if err := app.Run(cfg); err != nil {
		log.Fatal(err)
	}
}
