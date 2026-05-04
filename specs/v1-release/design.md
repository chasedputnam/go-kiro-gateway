# Design: go-kiro-gateway v1.0.0 Release

## Overview

This document describes the technical design for shipping v1.0.0 of `go-kiro-gateway`. The work falls into five areas:

1. **Pre-release cleanup** — commit all in-progress changes, decide on untracked files, update `.gitignore`
2. **Docker fix** — replace the stale Python root `Dockerfile` with the correct Go multi-stage build
3. **CI: Docker publish** — verify the existing `docker.yml` workflow already handles `v*` tag pushes correctly
4. **CI: GitHub Release** — add a new `release.yml` workflow that builds cross-platform binaries and creates a GitHub Release
5. **README update** — update version references, Docker pull commands, and binary install instructions

---

## Architecture

The release pipeline is entirely CI-driven. No external tooling (goreleaser, etc.) is introduced — the existing `Makefile` `build-all` target already produces all five platform binaries. The new `release.yml` workflow calls that target directly.

```
git push v1.0.0 tag
        │
        ├─► docker.yml (existing)
        │     test → build → push to GHCR
        │     tags: 1.0.0, 1.0, latest
        │
        └─► release.yml (new)
              build-all (5 binaries via Makefile)
              gh release create v1.0.0
              upload binary assets
```

---

## Components and Changes

### 1. Pre-release cleanup

**Tracked modified files to commit:**

All 16 currently modified tracked files are committed together in a single pre-release commit. They fall into two logical groups:

- **Bug fixes** (streaming error handling, zerolog migration): `gateway/internal/streaming/anthropic.go`, `openai.go`, `core.go`, `anthropic_test.go`, `openai_test.go`, `gateway/internal/auth/auth.go`, `aws_sso.go`, `kiro_desktop.go`, `sqlite.go`, `gateway/internal/config/config.go`, `gateway/internal/converter/anthropic.go`, `openai.go`, `core.go`, `gateway/internal/errors/kiro.go`, `gateway/internal/tokenizer/tokenizer.go`
- **README**: `README.md`

**Untracked files — disposition:**

| File/Dir | Decision | Rationale |
|---|---|---|
| `specs/` | Commit | Spec artifacts belong in the repo |
| `AI_SDLC.md` | Commit | Project-level documentation |
| `CLAUDE.md` | Commit | Agent persona config, useful for contributors |
| `.agents/` | Commit | Agent persona definitions |
| `.claude/` | Commit | Claude project config |
| `gateway/.env.bkp` | `.gitignore` | Backup env file, may contain secrets |

`.gitignore` additions:
```
gateway/.env.bkp
*.env.bkp
```

---

### 2. Root Dockerfile fix

The root `Dockerfile` is currently a Python image left over from an earlier iteration. The CI workflow (`docker.yml`) uses `context: .` (repo root), so it picks up this file. The fix replaces it with a copy of `gateway/Dockerfile` adjusted for the root build context.

**Key difference between root and `gateway/Dockerfile`:** The `gateway/Dockerfile` is designed to be built with `context: gateway/` (so `COPY go.mod go.sum` and `COPY . .` refer to the `gateway/` directory). The root `Dockerfile` must use `context: .` (repo root), so the `COPY` paths need a `gateway/` prefix.

**Root `Dockerfile` design:**

```dockerfile
# Build stage
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src

# Cache module downloads (paths relative to repo root context)
COPY gateway/go.mod gateway/go.sum ./
RUN go mod download

# Copy gateway source
COPY gateway/ .

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux \
    go build -trimpath \
    -ldflags "-X main.version=${VERSION} -s -w" \
    -o /go-kiro-gateway ./cmd/gateway

# Runtime stage
FROM alpine:3.21

RUN apk add --no-cache ca-certificates curl

RUN addgroup -S kiro && adduser -S -G kiro kiro

COPY --from=builder /go-kiro-gateway /usr/local/bin/go-kiro-gateway

RUN mkdir -p /app/debug_logs && chown -R kiro:kiro /app/debug_logs

WORKDIR /app
USER kiro
EXPOSE 8000

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["curl", "-sf", "http://localhost:8000/health"]

ENTRYPOINT ["go-kiro-gateway"]
```

**`docker-compose.yml`:** The existing `docker-compose.yml` lives in `gateway/` and already references the correct `gateway/Dockerfile` with `context: .` (relative to `gateway/`). No changes needed there.

---

### 3. CI: Docker publish verification

The existing `.github/workflows/docker.yml` already satisfies all of Requirement 5:

- Triggers on `v*` tag pushes ✓
- Uses `docker/metadata-action` with `type=semver,pattern={{version}}` and `type=semver,pattern={{major}}.{{minor}}` ✓
- Pushes `latest` on default branch ✓
- Builds `linux/amd64` and `linux/arm64` via `platforms: linux/amd64,linux/arm64` ✓
- Skips push on pull requests via `if: github.event_name != 'pull_request'` ✓

**One gap:** The `build` job currently builds with `context: .` but the `Dockerfile` at root is Python. Once the root `Dockerfile` is fixed (change 2 above), this workflow will work correctly with no further changes.

---

### 4. CI: GitHub Release workflow

New file: `.github/workflows/release.yml`

**Trigger:** `push` on tags matching `v*` only. No branch triggers.

**Permissions:** `contents: write` (required to create releases and upload assets).

**Steps:**

1. `actions/checkout@v4` with `fetch-depth: 0` (needed for `git describe` and release notes)
2. `actions/setup-go@v5` with `go-version-file: gateway/go.mod`
3. Extract version from tag: `VERSION=${GITHUB_REF_NAME#v}` (strips leading `v` for ldflags)
4. Run `make build-all VERSION=$VERSION` from `gateway/` directory — produces 5 binaries in `gateway/build/`
5. `softprops/action-gh-release@v2` — creates the GitHub Release and uploads all binaries

**Version extraction rationale:** The Makefile `LDFLAGS` injects `-X main.version=$(VERSION)`. The tag is `v1.0.0` but `main.version` should be `1.0.0` (no `v` prefix), matching semver convention. The `#v` strip handles this.

**Release notes:** `softprops/action-gh-release` supports `generate_release_notes: true` which uses GitHub's auto-generated notes (commits since previous tag). No custom changelog tooling needed.

**Workflow design:**

```yaml
name: Release

on:
  push:
    tags: ['v*']

permissions:
  contents: write

jobs:
  release:
    name: Build and Release
    runs-on: ubuntu-latest

    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - uses: actions/setup-go@v5
        with:
          go-version-file: gateway/go.mod
          cache-dependency-path: gateway/go.sum

      - name: Build cross-platform binaries
        working-directory: gateway
        run: |
          VERSION=${GITHUB_REF_NAME#v}
          make build-all VERSION=$VERSION

      - name: Create GitHub Release
        uses: softprops/action-gh-release@v2
        with:
          generate_release_notes: true
          files: |
            gateway/build/go-kiro-gateway-linux-amd64
            gateway/build/go-kiro-gateway-linux-arm64
            gateway/build/go-kiro-gateway-darwin-amd64
            gateway/build/go-kiro-gateway-darwin-arm64
            gateway/build/go-kiro-gateway-windows-amd64.exe
```

---

### 5. README update

The README needs the following changes:

- **Version badge / header:** Add a GitHub release badge pointing to the latest release
- **Docker pull command:** Update to reference `ghcr.io/chasedputnam/go-kiro-gateway:latest` (or `:1.0.0`)
- **Binary install section:** Add a section showing how to download from the GitHub Releases page with `curl` or `wget` for each platform
- **Remove Python references:** Remove any mention of `requirements.txt`, `python main.py`, or the Python `Dockerfile`
- **docker-compose note:** Clarify that `docker-compose.yml` lives in `gateway/` and should be run from there

---

## Data Models

No data model changes. This is purely infrastructure and tooling work.

---

## Error Handling

**Docker build failures:** The `docker.yml` workflow already has `exit-code: '0'` on Trivy so a vulnerability scan failure does not block the release. The image test step (`curl -f http://localhost:8000/health`) will catch a broken binary before push.

**Release workflow failures:** If `make build-all` fails, the `softprops/action-gh-release` step is never reached — no partial release is created. GitHub Actions will mark the workflow run as failed and notify the maintainer.

**Tag already exists:** If `v1.0.0` is pushed twice, `softprops/action-gh-release` will fail with "release already exists". This is the correct behavior — releases are immutable.

---

## Testing Strategy

### Pre-merge verification
- `go test ./... -timeout 120s` — already passing, run before committing
- `docker build -t kiro-gateway:test .` from repo root — verifies the fixed root `Dockerfile` builds successfully
- `docker run` smoke test against `/health` endpoint — same test the CI workflow runs

### CI verification (post-push)
- `docker.yml` test job runs the full Go test suite on every push
- `docker.yml` build job runs the Docker image smoke test
- `release.yml` is only triggered by tag push — validate by pushing `v1.0.0` and confirming both workflows go green and the GitHub Release page shows all 5 binary assets

### Manual checks
- Pull `ghcr.io/chasedputnam/go-kiro-gateway:1.0.0` and run `/health` — confirms GHCR publish worked
- Download a binary from the GitHub Release page and run `./go-kiro-gateway --version` (or equivalent) — confirms version injection worked
- `git describe --tags --always` from `gateway/` resolves to `v1.0.0`
