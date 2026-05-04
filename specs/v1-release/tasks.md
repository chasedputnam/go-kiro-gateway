# Tasks: go-kiro-gateway v1.0.0 Release

Implementation plan based on the requirements and design documents.

---

- [x] 1. Update `.gitignore` and stage untracked files for commit
  - Add `gateway/.env.bkp` and `*.env.bkp` patterns to `.gitignore`
  - Verify `.agents/`, `.claude/`, `AI_SDLC.md`, `CLAUDE.md`, and `specs/` are ready to commit
  - References: Requirement 2.1, 2.2, 2.3

- [x] 2. Commit all in-progress changes to main
  - Stage all 16 modified tracked files plus the newly tracked untracked files decided in task 1
  - Write a commit message describing the bug fixes (streaming error handling, zerolog migration) and release prep
  - Push to main and confirm CI passes (tests green, Docker build succeeds)
  - References: Requirement 1.1, 1.2, 1.3

- [x] 3. Replace the root `Dockerfile` with the correct Go multi-stage build
  - Overwrite `/Dockerfile` with a multi-stage Go build using `context: .` (repo root) path conventions
  - `COPY gateway/go.mod gateway/go.sum ./` and `COPY gateway/ .` in the builder stage
  - Runtime stage: `alpine:3.21`, non-root `kiro` user, `curl` healthcheck, same as `gateway/Dockerfile`
  - Inject `ARG VERSION=dev` and `-X main.version=${VERSION}` ldflags
  - References: Requirement 3.1, 3.2, 3.3

- [x] 4. Verify `docker-compose.yml` is correct
  - Confirm `gateway/docker-compose.yml` references the correct Dockerfile and build context
  - Confirm the healthcheck uses `curl -sf http://localhost:8000/health`
  - Make any corrections needed; no changes expected based on design review
  - References: Requirement 4.1, 4.2, 4.3

- [x] 5. Verify the existing `docker.yml` CI workflow satisfies Docker publish requirements
  - Confirm `on.push.tags: ['v*']` trigger is present
  - Confirm `docker/metadata-action` produces `type=semver,pattern={{version}}`, `type=semver,pattern={{major}}.{{minor}}`, and `latest` tags
  - Confirm `platforms: linux/amd64,linux/arm64` is set on the push step
  - Confirm `if: github.event_name != 'pull_request'` guards the push step
  - Document any gaps found and fix them
  - References: Requirement 5.1, 5.2, 5.3, 5.4, 5.5

- [x] 6. Create `.github/workflows/release.yml` for cross-platform binary releases
  - Trigger: `on.push.tags: ['v*']` only — no branch triggers
  - Permissions: `contents: write`
  - Steps: checkout with `fetch-depth: 0`, setup-go from `gateway/go.mod`, extract `VERSION=${GITHUB_REF_NAME#v}`, run `make build-all VERSION=$VERSION` from `gateway/`, upload with `softprops/action-gh-release@v2`
  - Set `generate_release_notes: true` on the release step
  - Attach all 5 binaries: `gateway/build/go-kiro-gateway-linux-amd64`, `-linux-arm64`, `-darwin-amd64`, `-darwin-arm64`, `-windows-amd64.exe`
  - References: Requirement 6.1, 6.2, 6.3, 6.4, 6.5

- [x] 7. Verify version injection and `go.mod` module path
  - Confirm `gateway/go.mod` module path is `github.com/chasedputnam/go-kiro-gateway/gateway`
  - Confirm `gateway/Makefile` `VERSION` derives from `git describe --tags --always --dirty`
  - Confirm `cmd/gateway/main.go` declares `var version = "dev"` and the build uses `-X main.version=$(VERSION)`
  - References: Requirement 7.1, 7.2, 7.3

- [x] 8. Update README for v1.0.0
  - Add a GitHub release badge (shields.io or GitHub native) to the header
  - Update Docker usage section: `docker pull ghcr.io/chasedputnam/go-kiro-gateway:latest`
  - Add a binary install section with `curl`/`wget` download examples pointing to the GitHub Releases page for each platform
  - Remove all Python references (`requirements.txt`, `python main.py`, Python Dockerfile)
  - Clarify that `docker-compose.yml` lives in `gateway/` and should be run from there
  - References: Requirement 9.1, 9.2, 9.3, 9.4

- [x] 9. Commit Docker, CI, and README changes and push to main
  - Stage and commit the root `Dockerfile`, `release.yml`, and updated `README.md`
  - Push and confirm CI passes on main before tagging
  - References: Requirement 1.3, 3.3, 6.5

- [ ] 10. Tag `v1.0.0` and push
  - Create annotated tag: `git tag -a v1.0.0 -m "Release v1.0.0"`
  - Push the tag: `git push origin v1.0.0`
  - Confirm `docker.yml` and `release.yml` both trigger and complete successfully
  - Confirm the GitHub Release page shows all 5 binary assets
  - Confirm `ghcr.io/chasedputnam/go-kiro-gateway:1.0.0` and `:latest` are published to GHCR
  - References: Requirement 8.1, 8.2, 8.3
