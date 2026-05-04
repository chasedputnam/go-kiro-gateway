# Requirements Document

## Introduction

A shell script that automates first-time setup of the go-kiro-gateway service. It discovers Kiro authentication credentials from known filesystem locations, generates a `.env` file from `.env.example` if one does not exist, populates it with discovered values, and appends the required Claude Code environment variable exports to the current user's `~/.bashrc`. Supports Linux and macOS.

## Requirements

### Requirement 1: Credential Discovery

**User Story:** As a developer setting up the gateway, I want the script to automatically find my Kiro credentials so that I don't have to manually locate and copy credential file paths.

#### Acceptance Criteria

WHEN the script runs THEN it SHALL search the following locations in order:
1. `~/.kiro/` — for any `.json` files containing Kiro auth tokens
2. `~/.aws/sso/cache/` — for `kiro-auth-token.json` or any `.json` file containing a `refreshToken` field and an expires field that does not have a date and time in the past so it is still valid.

WHEN a credential file is found THEN the script SHALL set `KIRO_CREDS_FILE` to its absolute path.

WHEN multiple credential files are found THEN the script SHALL prefer `~/.kiro/` over `~/.aws/sso/cache/` and inform the user which file was selected.

WHEN no credential file is found THEN the script SHALL warn the user and leave `KIRO_CREDS_FILE` empty, prompting the user to set it manually.

---

### Requirement 2: .env File Generation

**User Story:** As a developer, I want the script to create a `.env` file pre-populated with discovered values so that I can start the gateway with minimal manual editing.

#### Acceptance Criteria

WHEN a `.env` file already exists in the project root THEN the script SHALL NOT overwrite it and SHALL notify the user that setup was skipped.

WHEN no `.env` file exists THEN the script SHALL copy `.env.example` to `.env`.

WHEN `KIRO_CREDS_FILE` was discovered THEN the script SHALL set that value in the generated `.env`.

WHEN `PROFILE_ARN` has not been populated THEN the script SHALL run `kiro-cli whoami` command and get the third line string which is the PROFILE_ARN.

WHEN `PROFILE_ARN` cannot be discovered automatically THEN the script SHALL leave it as a placeholder and print a clear message telling the user to fill it in manually.

WHEN the `.env` file is written THEN it SHALL contain at minimum: `PROXY_API_KEY` (a randomly generated secure value), `KIRO_CREDS_FILE` (if discovered), and `PROFILE_ARN` (placeholder if not found).

---

### Requirement 3: PROXY_API_KEY Generation

**User Story:** As a developer, I want a secure API key generated for me so that I don't have to think of one myself.

#### Acceptance Criteria

WHEN generating `PROXY_API_KEY` THEN the script SHALL produce a cryptographically random alphanumeric string of at least 24 characters using `openssl rand` or `/dev/urandom` as a fallback.

WHEN the key is generated THEN the script SHALL print it to the terminal so the user can record it.

---

### Requirement 4: Claude Code Environment Variable Export

**User Story:** As a developer using Claude Code, I want the required environment variables exported in my shell so that Claude Code can connect to the gateway without additional configuration.

#### Acceptance Criteria

WHEN the script runs THEN it SHALL append the following exports to `~/.bashrc` if they are not already present:
- `ANTHROPIC_BASE_URL` set to `http://localhost:8000`
- `ANTHROPIC_API_KEY` set to the value of `PROXY_API_KEY`

WHEN an export line already exists in `~/.bashrc` THEN the script SHALL NOT add a duplicate.

WHEN exports are appended THEN the script SHALL add a comment block identifying them as added by this setup script.

WHEN the script completes THEN it SHALL remind the user to run `source ~/.bashrc` or open a new terminal.

---

### Requirement 5: Cross-Platform Support

**User Story:** As a developer on either Linux or macOS, I want the same script to work on my machine without modification.

#### Acceptance Criteria

WHEN running on macOS THEN the script SHALL also check `~/.bashrc` equivalent (`~/.bash_profile` or `~/.zshrc`) if `~/.bashrc` does not exist, and inform the user which file was updated.

WHEN running on Linux THEN the script SHALL target `~/.bashrc`.

WHEN platform-specific commands differ (e.g., `openssl`, `stat`) THEN the script SHALL detect the OS and use the appropriate variant.

IF a required tool (`openssl`, `jq`) is not available THEN the script SHALL fall back to a pure-shell alternative rather than failing.

---

### Requirement 6: User Feedback and Safety

**User Story:** As a developer, I want clear output from the script so that I know what was done and what still needs my attention.

#### Acceptance Criteria

WHEN the script completes THEN it SHALL print a summary of: what credential file was found, what was written to `.env`, and what was added to the shell rc file.

WHEN any step is skipped (e.g., `.env` already exists) THEN the script SHALL clearly say so.

WHEN the script makes any change THEN it SHALL be non-destructive — existing files SHALL NOT be overwritten without explicit user confirmation.

WHEN the script encounters an unrecoverable error THEN it SHALL exit with a non-zero status and a descriptive message.
