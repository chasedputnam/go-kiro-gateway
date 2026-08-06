# Changelog

All notable changes to go-kiro-gateway are documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

### Fixed
- Preserve full `Write` tool inputs in Kiro conversation history instead of replacing file content with a synthetic `File written` message
- Stream tool-call arguments incrementally in bounded SSE deltas for both OpenAI and Anthropic clients, including large file writes
- Treat tool-call activity as the first streamed token so long-running writes do not trigger a false first-token timeout

---

## [2.0.0] - 2025-05-28

### Added
- OpenAI-compatible `/v1/chat/completions` endpoint
- Anthropic-compatible `/v1/messages` endpoint
- Full SSE streaming support
- Tool / function calling support
- Vision (image) support
- Extended thinking / reasoning support
- Four credential modes: JSON credentials file, environment variables, AWS SSO cache, kiro-cli SQLite database
- Automatic token refresh before expiration
- Smart model name normalization (handles `-`, `.`, and versioned suffixes)
- Payload size management — auto-compacts Write tool history and caps oversized tool results
- Retry logic with backoff on 403, 429, and 5xx responses
- HTTP/SOCKS5 proxy support via `PROXY_URL`
- Docker image published to GitHub Container Registry (`ghcr.io/chasedputnam/go-kiro-gateway`)
- Multi-platform Docker builds: `linux/amd64`, `linux/arm64`
- Cross-compiled release binaries: Linux, macOS (Intel + Apple Silicon), Windows
- Debug logging modes: `errors`, `full`, `full-pretty`
- `DEBUG_SAVE_FILES=true` to persist request/response payloads to `debug_logs/`
- Dependabot for Go modules and Docker base image updates
- GitHub Actions: release workflow, Docker build/push workflow, CI lint+test workflow

### Models supported at launch
- Claude Opus 4.x
- Claude Sonnet 4.x
- Claude Haiku 4.x
- DeepSeek v3.2
- MiniMax M2.1
- Qwen3-Coder-Next

---

[Unreleased]: https://github.com/chasedputnam/go-kiro-gateway/compare/HEAD...HEAD
[2.0.0]: https://github.com/chasedputnam/go-kiro-gateway/releases/tag/v2.0.0
