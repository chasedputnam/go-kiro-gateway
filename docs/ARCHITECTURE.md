# Architectural Overview: Go Kiro Gateway

## 1. System Purpose and Goals

Go Kiro Gateway is a high-level proxy implementing the **"Adapter"** structural design pattern. It translates requests between OpenAI/Anthropic client formats and the upstream Kiro API (Amazon Q Developer / AWS CodeWhisperer).

This Go implementation produces a **single compiled binary** with zero runtime dependencies.

### Supported API Formats

| API | Endpoints | Status |
|-----|-----------|--------|
| **OpenAI** | `/v1/models`, `/v1/chat/completions` | ✅ Supported |
| **Anthropic** | `/v1/messages` | ✅ Supported |

### Architectural Model

```
┌────────────────────────────────────────────────────────────────┐
│                          Clients                               │
│  ┌─────────────────────┐       ┌─────────────────────┐         │
│  │  OpenAI SDK/Tools   │       │ Anthropic SDK/Tools │         │
│  │  (Cursor, Cline,    │       │ (Claude Code,       │         │
│  │   Continue, etc.)   │       │  Anthropic SDK)     │         │
│  └──────────┬──────────┘       └──────────┬──────────┘         │
└─────────────┼──────────────────────────────┼───────────────────┘
              │                              │
              ▼                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                   Go Kiro Gateway (Go Binary)                   │
│  ┌─────────────────────┐       ┌─────────────────────┐          │
│  │  OpenAI Adapter     │       │  Anthropic Adapter  │          │
│  │  /v1/chat/...       │       │  /v1/messages       │          │
│  └──────────┬──────────┘       └──────────┬──────────┘          │
│             └──────────────┬───────────────┘                    │
│                            ▼                                    │
│             ┌─────────────────────────────┐                     │
│             │      Core Layer             │                     │
│             │  (Shared conversion logic)  │                     │
│             └──────────────┬──────────────┘                     │
└────────────────────────────┼────────────────────────────────────┘
                             │
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│                        Kiro API                                 │
│              (AWS CodeWhisperer Backend)                        │
└─────────────────────────────────────────────────────────────────┘
```

Both APIs work simultaneously on the same server without any configuration switching.

## 2. Technology Stack

| Component | Go Implementation | Python Original |
|-----------|-------------------|-----------------|
| **Language** | Go 1.25+ | Python 3.10+ |
| **Router** | chi v5 | FastAPI |
| **Logging** | zerolog | loguru |
| **Token counting** | go-tiktoken (cl100k_base) | tiktoken |
| **SQLite** | modernc.org/sqlite (pure Go) | sqlite3 |
| **Env loading** | godotenv | python-dotenv |
| **HTTP client** | net/http (stdlib) | httpx |
| **Concurrency** | goroutines + sync.Mutex | asyncio + asyncio.Lock |
| **Streaming** | http.Flusher + channels | StreamingResponse + AsyncGenerator |
| **Build** | Single static binary | Python + pip dependencies |

### Key Design Decisions

1. **`net/http` + `chi`**: Go's standard `net/http` is production-grade. `chi` adds lightweight routing and middleware composition without framework lock-in.
2. **Interfaces for all boundaries**: Every component that touches I/O is defined by an interface, enabling table-driven tests with mock implementations.
3. **No global state**: All dependencies are injected via constructor functions. The `Server` struct owns the dependency graph.
4. **`context.Context` everywhere**: Every request handler, HTTP call, and token refresh carries a context for cancellation and timeout propagation.
5. **Pure-Go SQLite**: Avoids CGO dependency, enabling static cross-compilation for all target platforms.
6. **Streaming via `http.Flusher`**: SSE responses use `http.Flusher` to push chunks to clients immediately.

## 3. Project Structure

```
gateway/
├── cmd/gateway/
│   └── main.go                    # Entry point, CLI parsing, DI wiring
├── internal/
│   ├── config/
│   │   └── config.go              # Config struct, env loading, validation
│   ├── auth/
│   │   ├── auth.go                # AuthManager interface + implementation
│   │   ├── kiro_desktop.go        # Kiro Desktop refresh logic
│   │   ├── aws_sso.go             # AWS SSO OIDC refresh logic
│   │   └── sqlite.go              # SQLite credential loading
│   ├── cache/
│   │   └── cache.go               # ModelInfoCache with RWMutex
│   ├── resolver/
│   │   └── resolver.go            # ModelResolver + normalize functions
│   ├── client/
│   │   └── client.go              # KiroHTTPClient with retry logic
│   ├── converter/
│   │   ├── core.go                # UnifiedMessage, payload building
│   │   ├── openai.go              # OpenAI → Unified → Kiro
│   │   └── anthropic.go           # Anthropic → Unified → Kiro
│   ├── streaming/
│   │   ├── core.go                # KiroEvent, stream parsing, retry
│   │   ├── openai.go              # OpenAI SSE formatter
│   │   ├── anthropic.go           # Anthropic SSE formatter
│   │   └── collect.go             # Non-streaming response collection
│   ├── parser/
│   │   ├── eventstream.go         # AWS Event Stream binary parser
│   │   └── bracket.go             # Bracket-style tool call parser
│   ├── thinking/
│   │   └── parser.go              # ThinkingParser FSM
│   ├── tokenizer/
│   │   └── tokenizer.go           # Token counting with go-tiktoken
│   ├── debug/
│   │   ├── logger.go              # DebugLogger (off/errors/all modes)
│   │   └── middleware.go          # Debug middleware
│   ├── errors/
│   │   ├── network.go             # Network error classification
│   │   ├── kiro.go                # Kiro API error enhancement
│   │   └── validation.go          # Request validation errors
│   ├── truncation/
│   │   ├── state.go               # In-memory truncation state cache
│   │   └── recovery.go            # Synthetic message generation
│   ├── models/
│   │   ├── openai.go              # OpenAI request/response structs
│   │   ├── anthropic.go           # Anthropic request/response structs
│   │   └── kiro.go                # Kiro API payload structs
│   ├── middleware/
│   │   ├── cors.go                # CORS middleware
│   │   └── auth.go                # API key validation middleware
│   ├── logging/
│   │   └── logging.go             # zerolog initialization
│   └── server/
│       ├── server.go              # HTTP server setup, lifecycle
│       ├── routes_openai.go       # OpenAI route handlers
│       └── routes_anthropic.go    # Anthropic route handlers
├── go.mod
├── go.sum
├── Makefile                       # Build targets for all platforms
├── Dockerfile                     # Multi-stage Docker build
└── docker-compose.yml             # Container orchestration
```

### Organization Principle: Shared Core + Thin Adapters

| Layer | Purpose | Packages |
|-------|---------|----------|
| **Shared Layer** | Infrastructure independent of API format | `auth`, `client`, `cache`, `parser`, `tokenizer`, `logging` |
| **Core Layer** | Shared business logic for conversion | `converter/core.go`, `streaming/core.go` |
| **API Layer** | Thin adapters for specific formats | `converter/openai.go`, `converter/anthropic.go`, `streaming/openai.go`, `streaming/anthropic.go` |

## 4. Core Interfaces

All external boundaries are defined by interfaces for testability:

```go
// auth.AuthManager — token lifecycle management
type AuthManager interface {
    GetAccessToken(ctx context.Context) (string, error)
    ForceRefresh(ctx context.Context) error
    AuthType() AuthType
    ProfileARN() string
    Fingerprint() string
    APIHost() string
    QHost() string
}

// cache.ModelCache — thread-safe model metadata storage
type ModelCache interface {
    Update(models []ModelInfo)
    Get(modelID string) *ModelInfo
    IsValidModel(modelID string) bool
    GetMaxInputTokens(modelID string) int
    GetAllModelIDs() []string
    AddHiddenModel(displayName, internalID string)
}

// client.KiroClient — HTTP communication with retry
type KiroClient interface {
    RequestWithRetry(ctx context.Context, method, url string, payload any, stream bool) (*http.Response, error)
}

// resolver.Resolver — model name resolution pipeline
type Resolver interface {
    Resolve(externalModel string) ModelResolution
    GetAvailableModels() []string
}

// debug.DebugLogger — request/response debug logging
type DebugLogger interface {
    PrepareNewRequest()
    LogRequestBody(body []byte)
    LogKiroRequestBody(body []byte)
    LogRawChunk(chunk []byte)
    LogModifiedChunk(chunk []byte)
    LogAppMessage(msg string)
    FlushOnError(statusCode int, errorMessage string)
    DiscardBuffers()
}
```

## 5. Dependency Injection

All dependencies are wired in `cmd/gateway/main.go` at startup:

```
Config → AuthManager → HTTPClient
Config → ModelCache → ModelResolver
Config → DebugLogger
Config → TruncationState

All → Server
```

The startup flow:
1. Load config (`config.Load()`)
2. Initialize logging (`logging.Init()`)
3. Initialize auth manager (`auth.NewAuthManager()`)
4. Initialize model cache + load models from Kiro API (fallback to hardcoded list)
5. Add hidden models to cache
6. Initialize resolver (`resolver.New()`)
7. Initialize HTTP client (`client.NewKiroClient()`)
8. Initialize debug logger (`debug.NewDebugLogger()`)
9. Initialize truncation state (`truncation.NewState()`)
10. Create server (`server.New()`)
11. Print startup banner
12. Start server with graceful shutdown (SIGINT/SIGTERM)

## 6. Component Details

### 6.1 Configuration (`internal/config/`)

A single `Config` struct populated from environment variables, `.env` file, and CLI flags.

**Priority**: CLI flags → environment variables → defaults.

Key settings include server host/port, authentication credentials, proxy URL, timeouts, fake reasoning, truncation recovery, debug mode, model aliases, and hidden models.

### 6.2 Authentication (`internal/auth/`)

Supports four credential sources (in priority order):
1. **JSON file** (`KIRO_CREDS_FILE`)
2. **Environment variable** (`REFRESH_TOKEN`)
3. **SQLite database** (`KIRO_CLI_DB_FILE`)
4. **Enterprise Kiro IDE** (device registration from `~/.aws/sso/cache/`)

Auth type auto-detection: `clientId` + `clientSecret` present → AWS SSO OIDC, otherwise → Kiro Desktop.

Token refresh uses `sync.Mutex` with a double-check pattern for thread safety.

### 6.3 Model Resolution (`internal/resolver/`)

5-layer resolution pipeline:
1. Resolve aliases
2. Normalize name (regex: dashes→dots, strip dates, handle inverted format)
3. Check dynamic cache (from Kiro API)
4. Check hidden models (manual config)
5. Pass-through (let Kiro decide)

Key principle: **This is a gateway, not a gatekeeper.**

### 6.4 HTTP Client (`internal/client/`)

Retry logic:
- **403**: Force token refresh, retry
- **429**: Exponential backoff (1s, 2s, 4s)
- **5xx**: Exponential backoff
- **Timeouts**: Exponential backoff

Per-request `http.Client` for streaming (prevents CLOSE_WAIT leaks). Shared pooled client for non-streaming.

### 6.5 Converters (`internal/converter/`)

- `core.go`: `UnifiedMessage`, `BuildKiroPayload()`, JSON schema sanitization, tool description handling
- `openai.go`: OpenAI → Unified → Kiro (system prompt extraction, Cursor flat format)
- `anthropic.go`: Anthropic → Unified → Kiro (content block lists, prompt caching)

### 6.6 Streaming (`internal/streaming/`)

- `core.go`: `ParseKiroStream()` using Go channels as async generator equivalent. First-token timeout with retry.
- `openai.go`: `StreamToOpenAI()` — `data: {json}\n\n` chunks via `http.Flusher`
- `anthropic.go`: `StreamToAnthropic()` — `event: type\ndata: {json}\n\n` chunks
- `collect.go`: Non-streaming response collection

### 6.7 Parsers (`internal/parser/`)

- `eventstream.go`: AWS Event Stream binary parser with bracket counting and content deduplication
- `bracket.go`: Bracket-style tool call parser (`[Called func with args: {...}]`)

### 6.8 Thinking Parser (`internal/thinking/`)

Finite state machine with three states: `PreContent`, `InThinking`, `Streaming`. Detects opening tags at response start with cautious buffering.

### 6.9 Token Counter (`internal/tokenizer/`)

Uses `go-tiktoken` with `cl100k_base` encoding. Applies Claude correction factor (1.15x). Calculates prompt tokens from context usage percentage.

### 6.10 Debug Logger (`internal/debug/`)

Three modes: `off`, `errors`, `all`. In `errors` mode, buffers data and flushes to files only on 4xx/5xx. Thread-safe via `sync.Mutex`.

### 6.11 Truncation Recovery (`internal/truncation/`)

In-memory cache with one-time retrieval (get-and-delete). Detects content and tool call truncation, generates synthetic recovery messages.

### 6.12 Error Handling (`internal/errors/`)

- Network error classification with troubleshooting steps
- Kiro API error enhancement with user-friendly messages
- Format-specific validation error responses (OpenAI/Anthropic)

### 6.13 Server (`internal/server/`)

Wires the `chi` router with middleware stack (CORS → Auth → Debug), registers all route groups, and manages graceful shutdown via `http.Server.Shutdown()`.

## 7. Graceful Lifecycle Management

The gateway handles SIGINT and SIGTERM signals:

1. Stop accepting new connections
2. Wait for in-flight requests to complete (30-second timeout)
3. Release resources (HTTP client connections)
4. Exit cleanly

```go
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
defer stop()

// ... start server in goroutine ...

<-ctx.Done()
srv.Shutdown(30 * time.Second)
```

## 8. Build and Deployment

### Building

```bash
cd gateway

# Local build
make build

# Cross-compile for all platforms
make build-all

# Run tests
make test
```

### Docker

Multi-stage Dockerfile:
- **Build stage**: `golang:1.25-alpine` — compiles static binary with `CGO_ENABLED=0`
- **Runtime stage**: `alpine:3.21` — minimal image (~10MB) with only the binary, CA certs, and curl for health checks

```bash
cd gateway
docker-compose up -d
```

### Version Injection

Version is embedded at compile time via ldflags:

```bash
go build -ldflags "-X main.version=1.2.3" ./cmd/gateway
```

## 9. API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/` | GET | Health check (status, message, version) |
| `/health` | GET | Detailed health check (status, timestamp, version) |
| `/v1/models` | GET | List available models (OpenAI format) |
| `/v1/chat/completions` | POST | OpenAI Chat Completions (streaming/non-streaming) |
| `/v1/messages` | POST | Anthropic Messages API (streaming/non-streaming) |

### Authentication

- **OpenAI endpoints**: `Authorization: Bearer {PROXY_API_KEY}`
- **Anthropic endpoint**: `x-api-key: {PROXY_API_KEY}` or `Authorization: Bearer {PROXY_API_KEY}`

## 10. Data Flow

### Streaming Request Flow

```
Client → Middleware (CORS → Auth → Debug)
       → Route Handler
       → Converter (OpenAI/Anthropic → Kiro payload)
       → HTTP Client (with retry + token refresh)
       → Kiro API
       → Stream Parser (AWS Event Stream → KiroEvent channel)
       → Thinking Parser FSM
       → SSE Formatter (OpenAI/Anthropic format)
       → Client (data: {...}\n\n)
```

### Non-Streaming Request Flow

Same as streaming, but the response is collected into a complete JSON object before returning to the client.

## 11. Testing

All tests use Go's standard `testing` package with table-driven patterns. External I/O is mocked via interfaces.

```bash
cd gateway
go test -race ./...
```

Test coverage includes:
- Configuration loading and validation
- Authentication (all four methods)
- Model resolution (all normalization patterns)
- HTTP client retry logic
- Request conversion (OpenAI and Anthropic)
- Streaming (SSE formatting, thinking extraction)
- Error handling and classification
- Graceful shutdown lifecycle
