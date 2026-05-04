# Requirements: go-kiro-gateway v1.0.0 Release

## Introduction

This spec covers all work required to ship the first stable release (v1.0.0) of `go-kiro-gateway` to GitHub. The release includes: committing current in-progress changes, fixing broken root-level Docker artifacts, wiring up CI to publish the Docker image and cross-platform binaries on tag push, tagging `v1.0.0`, and ensuring the README accurately reflects the released version.

---

## Requirements

### Requirement 1 — Commit in-progress changes

**User Story:** As a maintainer, I want all current changes committed to main before tagging, so that the release reflects the latest state of the codebase.

#### Acceptance Criteria

1.1. WHEN the release is prepared THEN the repository SHALL have no uncommitted modifications to tracked files (`README.md`, `gateway/internal/config/config.go`, `gateway/internal/converter/core.go`, `gateway/internal/streaming/anthropic.go`, `gateway/internal/streaming/anthropic_test.go`, `gateway/internal/streaming/openai.go`, `gateway/internal/streaming/openai_test.go`).

1.2. WHEN files are staged and committed THEN the commit message SHALL follow the project's existing convention and describe the changes included.

1.3. WHEN the commit is pushed THEN CI SHALL pass (tests green, Docker build succeeds).

---

### Requirement 2 — Decide which untracked files to include

**User Story:** As a maintainer, I want to explicitly decide which untracked files belong in the repo, so that the release does not accidentally include or omit important files.

#### Acceptance Criteria

2.1. WHEN untracked files are evaluated THEN each of `.agents/`, `.claude/`, `AI_SDLC.md`, `CLAUDE.md`, `gateway/.env.bkp`, and `specs/` SHALL be either committed or added to `.gitignore`.

2.2. IF a file contains secrets or local-only configuration THEN it SHALL be added to `.gitignore` rather than committed.

2.3. WHEN the decision is made THEN `.gitignore` SHALL be updated if new patterns are needed and committed.

---

### Requirement 3 — Fix the root-level Dockerfile

**User Story:** As a developer, I want the root-level `Dockerfile` to build the Go gateway, so that `docker build .` from the repo root produces a working image.

#### Acceptance Criteria

3.1. WHEN `docker build .` is run from the repo root THEN it SHALL produce a working Go gateway image, not a Python image.

3.2. WHEN the root `Dockerfile` is fixed THEN it SHALL be consistent with `gateway/Dockerfile` (multi-stage Go build, Alpine runtime, non-root user).

3.3. WHEN the CI workflow (`docker.yml`) builds with `context: .` THEN it SHALL use the corrected root `Dockerfile`.

---

### Requirement 4 — Fix docker-compose.yml

**User Story:** As a developer, I want `docker-compose up` to start the gateway correctly, so that local development and testing via Compose works out of the box.

#### Acceptance Criteria

4.1. WHEN `docker-compose up` is run THEN the `kiro-gateway` service SHALL start and pass its health check.

4.2. WHEN the `docker-compose.yml` references a `Dockerfile` THEN it SHALL reference the correct Go Dockerfile (either `gateway/Dockerfile` with the correct build context, or the fixed root `Dockerfile`).

4.3. WHEN the health check runs THEN it SHALL use `curl` against `http://localhost:8000/health`, consistent with the Go gateway's `/health` endpoint.

---

### Requirement 5 — CI: publish Docker image on tag push

**User Story:** As a maintainer, I want the CI pipeline to automatically publish the Docker image to GHCR when a `v*` tag is pushed, so that every release has a corresponding versioned image.

#### Acceptance Criteria

5.1. WHEN a `v*` tag is pushed THEN the CI workflow SHALL build and push the Docker image to `ghcr.io/chasedputnam/go-kiro-gateway`.

5.2. WHEN the image is pushed THEN it SHALL be tagged with the full semver (e.g. `1.0.0`), the major.minor (e.g. `1.0`), and `latest`.

5.3. WHEN the image is built for a tag push THEN it SHALL be built for both `linux/amd64` and `linux/arm64`.

5.4. WHEN the CI workflow runs on a pull request THEN it SHALL NOT push any image to the registry.

5.5. IF the existing `docker.yml` already satisfies requirements 5.1–5.4 THEN no changes to the workflow are needed and this requirement is satisfied by verification.

---

### Requirement 6 — CI: GitHub Release with binary assets

**User Story:** As a user, I want to download a pre-built binary for my platform directly from the GitHub Release page, so that I can run the gateway without needing Go installed.

#### Acceptance Criteria

6.1. WHEN a `v*` tag is pushed THEN a GitHub Actions workflow SHALL build cross-platform binaries using the `build-all` Makefile target (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64).

6.2. WHEN binaries are built THEN each SHALL have the version injected via ldflags (`-X main.version=<tag>`).

6.3. WHEN the workflow completes THEN it SHALL create a GitHub Release for the tag and attach all five binaries as release assets.

6.4. WHEN the GitHub Release is created THEN it SHALL include auto-generated release notes from the git log since the previous tag.

6.5. WHEN the release workflow runs on a branch push or pull request THEN it SHALL NOT create a release or upload assets.

---

### Requirement 7 — Version injection and `go.mod` correctness

**User Story:** As a user, I want the gateway binary to report the correct version at runtime, so that I can confirm which release I am running.

#### Acceptance Criteria

7.1. WHEN the gateway is built with a `v*` tag THEN `main.version` SHALL be set to the tag value (e.g. `1.0.0`) via ldflags.

7.2. WHEN the gateway is built without a tag THEN `main.version` SHALL fall back to the git commit SHA with a `-dirty` suffix if there are uncommitted changes.

7.3. WHEN `go.mod` is inspected THEN the module path SHALL be `github.com/chasedputnam/go-kiro-gateway/gateway` and SHALL match the import paths used throughout the codebase.

---

### Requirement 8 — Tag v1.0.0 and push

**User Story:** As a maintainer, I want to create and push the `v1.0.0` git tag, so that the release workflows trigger and the version is permanently recorded in the repository history.

#### Acceptance Criteria

8.1. WHEN all prior requirements are satisfied and CI is green on main THEN the tag `v1.0.0` SHALL be created pointing at the HEAD commit on main.

8.2. WHEN the tag is pushed THEN the Docker publish workflow (Requirement 5) and the release binary workflow (Requirement 6) SHALL both trigger and complete successfully.

8.3. WHEN the tag is pushed THEN `git describe --tags --always` in the `gateway/` directory SHALL resolve to `v1.0.0`.

---

### Requirement 9 — Update README for v1.0.0

**User Story:** As a user evaluating the project, I want the README to reflect the current release version and accurate setup instructions, so that I can get started without confusion.

#### Acceptance Criteria

9.1. WHEN the README is updated THEN it SHALL reference `v1.0.0` in any version badges, Docker image pull commands, or binary download examples.

9.2. WHEN the README documents Docker usage THEN the image reference SHALL point to `ghcr.io/chasedputnam/go-kiro-gateway:1.0.0` (or `:latest`).

9.3. WHEN the README documents binary installation THEN it SHALL include download links or instructions referencing the GitHub Releases page.

9.4. WHEN the README is reviewed THEN any references to the incorrect Python `Dockerfile` or stale setup steps SHALL be removed or corrected.
