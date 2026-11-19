# Resultados de Benchmark — GoFastAPI

Ambiente:
- OS: Windows, goos: windows, goarch: amd64
- CPU: 13th Gen Intel(R) Core(TM) i5-1334U
- Go: 1.23.4

Comando: `go test -run='^$' -bench='Benchmark' -benchmem -benchtime=100ms .`

| Benchmark | ns/op | B/op | allocs/op |
|-----------|-------|------|-----------|
| BenchmarkHandlerDirect | 61,318 | 6,363 | 68 |
| BenchmarkRouterStatic | 79,965 | 6,993 | 75 |
| BenchmarkRouterDynamic | 74,375 | 7,796 | 81 |
| BenchmarkJSONResponse | 1,623 | 832 | 20 |
| BenchmarkMiddlewareChain | 263,334 | 872,319 | 139 |
| BenchmarkDI_Singleton | 127.3 | 32 | 1 |
| BenchmarkDI_Request | 87.63 | 32 | 1 |
| BenchmarkDI_Transient | 226.0 | 80 | 3 |
| BenchmarkRouterGroup | 53,294 | 7,906 | 83 |

## Observações

- **Rota estática vs dinâmica**: O overhead da rota dinâmica é mínimo (~7% a mais de ns/op), demonstrando a eficiência da trie de segmentos.
- **DI Singleton vs Request vs Transient**: Singleton e Request têm o mesmo custo (~87-127 ns/op, 1 alocação), enquanto Transient custa ~226 ns/op (3 alocações) pois cria nova instância a cada resolução.
- **Middleware chain**: O custo mais alto (~263 µs/op) reflete o encadeamento de 5 middlewares (Recovery, RequestID, CORS, Compression, Timeout) — esperado para cadeias completas.
- **JSON response**: Muito eficiente (~1.6 µs/op, 20 alocações) para serialização de payload médio.

Esses valores são baseline para detecção de regressão em releases futuras. Nenhum benchmark deve regressar >10% sem justificativa documentada.
