// Package gofast is a FastAPI-inspired web framework for Go.
//
// It provides trie-based routing, request-scoped dependency injection,
// middleware, OpenAPI 3.1 generation, WebSocket and SSE streaming, background
// tasks, health checks, observability helpers and a production server with
// TLS/mTLS, HTTP/2, h2c, multi-process workers and graceful shutdown.
//
// A minimal application:
//
//	app := New()
//	app.GET("/", func(ctx *Context) (any, error) {
//		return OK(map[string]string{"status": "ok"}), nil
//	})
//	log.Fatal(app.Run(DefaultServerConfig()))
package gofast
