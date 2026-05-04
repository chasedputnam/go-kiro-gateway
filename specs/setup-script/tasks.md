# Implementation Tasks

- [x] 1. Create setup.sh scaffold with OS detection and rc file selection
  - Create `setup.sh` at project root with shebang `#!/usr/bin/env bash` and `set -e`
  - Implement `detect_os()` using `uname -s`, sets `OS_TYPE` to `linux` or `macos`
  - Implement `select_rc_file()` — Linux targets `~/.bashrc`; macOS prefers `~/.zshrc`, then `~/.bash_profile`, then `~/.bashrc`
  - Add guard: exit 1 if `.env.example` is not found in current directory
  - References: Requirement 5, Requirement 6

- [x] 2. Implement PROXY_API_KEY generation
  - Implement `generate_api_key()` — primary path uses `openssl rand -base64 32 | tr -dc 'a-zA-Z0-9' | head -c 32`
  - Fallback: `/dev/urandom` read via `od -An -tx1` piped through `tr` and `head`, or `python3 -c "import secrets; print(secrets.token_urlsafe(24))"` as last resort
  - Print generated key to terminal
  - References: Requirement 3

- [x] 3. Implement credential discovery
  - Implement `discover_credentials()` — sets `CREDS_FILE` to absolute path or empty string
  - Step 1: scan `~/.kiro/` for any readable non-empty `.json` file
  - Step 2: check `~/.aws/sso/cache/kiro-auth-token.json` exact match
  - Step 3: scan `~/.aws/sso/cache/` for `.json` files containing `refreshToken` field with non-expired `expiresAt`/`expires` field
  - Expiry check: use `jq` if available; fallback to `grep` + `date` comparison (`date -d` on Linux, `date -j -f` on macOS)
  - If expiry parse fails, treat file as valid
  - Prefer `~/.kiro/` results over `~/.aws/sso/cache/` results; print which file was selected
  - Warn and set `CREDS_FILE=""` if nothing found
  - References: Requirement 1

- [x] 4. Implement PROFILE_ARN discovery via kiro-cli
  - Implement `discover_profile_arn()` — prints ARN string or empty
  - Run `kiro-cli whoami 2>/dev/null`, extract line 3 with `sed -n '3p'`
  - Return empty string if `kiro-cli` is not on `$PATH` or exits non-zero
  - References: Requirement 2

- [x] 5. Implement .env file writer
  - Implement `write_env()` — copies `.env.example` to `.env`, then patches values in place
  - If `.env` already exists: print notice and return early (do not modify)
  - Use `cp .env.example .env` to create the file
  - Patch `PROXY_API_KEY`: replace line matching `^PROXY_API_KEY=` with new value
  - Patch `KIRO_CREDS_FILE`: if discovered, uncomment and set `KIRO_CREDS_FILE="<path>"`; otherwise leave commented
  - Patch `PROFILE_ARN`: if discovered, replace line matching `^PROFILE_ARN=`; otherwise leave placeholder and warn
  - Use `sed -i ''` on macOS and `sed -i` on Linux for in-place edits
  - References: Requirement 2, Requirement 3

- [x] 6. Implement shell rc file patcher
  - Implement `patch_rc_file()` — appends exports to `RC_FILE` if not already present
  - Check for existing `ANTHROPIC_BASE_URL` export with `grep -q`
  - Check for existing `ANTHROPIC_API_KEY` export with `grep -q`
  - Only append lines that are missing
  - Wrap appended lines in comment block: `# Added by go-kiro-gateway setup.sh` ... `# End go-kiro-gateway setup`
  - Exit 1 with descriptive message if `RC_FILE` is not writable
  - References: Requirement 4, Requirement 5

- [x] 7. Implement summary printer and wire up main execution
  - Implement `print_summary()` — prints credential file status, `.env` write status, `PROFILE_ARN` status, rc file patch status
  - Always print reminder: `source <RC_FILE>` or open a new terminal
  - Call all functions in order from a `main()` function: `detect_os`, `select_rc_file`, `discover_credentials`, `generate_api_key`, `write_env`, `discover_profile_arn`, `patch_rc_file`, `print_summary`
  - Make script executable: `chmod +x setup.sh`
  - References: Requirement 6
