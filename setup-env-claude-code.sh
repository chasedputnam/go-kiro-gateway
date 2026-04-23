#!/usr/bin/env bash
set -e

# ─── Globals ────────────────────────────────────────────────────────────────
OS_TYPE=""
RC_FILE=""
CREDS_FILE=""
PROXY_API_KEY=""
PROFILE_ARN=""
ENV_WRITTEN=false
ENV_SKIPPED=false
RC_PATCHED=false
RC_SKIPPED=false

# ─── OS Detection & RC File Selection ───────────────────────────────────────

detect_os() {
    local uname
    uname="$(uname -s)"
    case "$uname" in
        Linux*)  OS_TYPE="linux" ;;
        Darwin*) OS_TYPE="macos" ;;
        *)
            echo "ERROR: Unsupported OS: $uname" >&2
            exit 1
            ;;
    esac
}

select_rc_file() {
    if [ "$OS_TYPE" = "linux" ]; then
        RC_FILE="$HOME/.bashrc"
    else
        if [ -f "$HOME/.zshrc" ]; then
            RC_FILE="$HOME/.zshrc"
        else
            RC_FILE="$HOME/.bash_profile"
        fi
    fi
}

# ─── Key Generation ─────────────────────────────────────────────────────────

generate_api_key() {
    local key=""
    if command -v openssl >/dev/null 2>&1; then
        key="$(openssl rand -base64 32 | tr -dc 'a-zA-Z0-9' | head -c 32)"
    elif [ -r /dev/urandom ]; then
        key="$(od -An -tx1 /dev/urandom | tr -dc 'a-zA-Z0-9' | head -c 32)"
    elif command -v python3 >/dev/null 2>&1; then
        key="$(python3 -c "import secrets; print(secrets.token_urlsafe(24))" | tr -dc 'a-zA-Z0-9' | head -c 32)"
    fi
    echo "$key"
}

# ─── Credential Discovery ───────────────────────────────────────────────────

# Returns epoch seconds for an ISO8601 date string, or empty on failure.
parse_expiry_epoch() {
    local date_str="$1"
    local epoch=""
    if [ "$OS_TYPE" = "linux" ]; then
        epoch="$(date -d "$date_str" +%s 2>/dev/null)" || true
    else
        # macOS BSD date — try a few common formats
        epoch="$(date -j -f "%Y-%m-%dT%H:%M:%SZ" "$date_str" +%s 2>/dev/null)" \
            || epoch="$(date -j -f "%Y-%m-%dT%H:%M:%S%z" "$date_str" +%s 2>/dev/null)" \
            || true
    fi
    echo "$epoch"
}

# Returns 0 (valid) or 1 (expired/unknown). Conservative: unknown = valid.
is_file_valid() {
    local file="$1"
    [ -r "$file" ] && [ -s "$file" ] || return 1

    local expiry_str=""
    if command -v jq >/dev/null 2>&1; then
        expiry_str="$(jq -r '.expiresAt // .expires // empty' "$file" 2>/dev/null)" || true
    else
        expiry_str="$(grep -o '"expiresAt" *: *"[^"]*"' "$file" 2>/dev/null | head -1 | grep -o '"[^"]*"$' | tr -d '"')" || true
        if [ -z "$expiry_str" ]; then
            expiry_str="$(grep -o '"expires" *: *"[^"]*"' "$file" 2>/dev/null | head -1 | grep -o '"[^"]*"$' | tr -d '"')" || true
        fi
    fi

    # No expiry field found — treat as valid
    [ -z "$expiry_str" ] && return 0

    local expiry_epoch now_epoch
    expiry_epoch="$(parse_expiry_epoch "$expiry_str")"
    # Parse failed — treat as valid
    [ -z "$expiry_epoch" ] && return 0

    now_epoch="$(date +%s)"
    [ "$expiry_epoch" -gt "$now_epoch" ]
}

discover_credentials() {
    CREDS_FILE=""

    # 1. Search ~/.kiro/ for any readable non-empty .json
    if [ -d "$HOME/.kiro" ]; then
        while IFS= read -r -d '' f; do
            if [ -r "$f" ] && [ -s "$f" ]; then
                CREDS_FILE="$f"
                echo "  Found credential file in ~/.kiro/: $CREDS_FILE"
                return
            fi
        done < <(find "$HOME/.kiro" -maxdepth 2 -name "*.json" -print0 2>/dev/null)
    fi

    # 2. Exact match in ~/.aws/sso/cache/
    local exact="$HOME/.aws/sso/cache/kiro-auth-token.json"
    if is_file_valid "$exact"; then
        CREDS_FILE="$exact"
        echo "  Found credential file: $CREDS_FILE"
        return
    fi

    # 3. Scan ~/.aws/sso/cache/ for any .json with refreshToken and valid expiry
    if [ -d "$HOME/.aws/sso/cache" ]; then
        while IFS= read -r -d '' f; do
            if grep -q '"refreshToken"' "$f" 2>/dev/null && is_file_valid "$f"; then
                CREDS_FILE="$f"
                echo "  Found credential file in ~/.aws/sso/cache/: $CREDS_FILE"
                return
            fi
        done < <(find "$HOME/.aws/sso/cache" -maxdepth 1 -name "*.json" -print0 2>/dev/null)
    fi

    echo "  WARNING: No valid Kiro credential file found."
    echo "           Set KIRO_CREDS_FILE manually in .env after setup."
}

# ─── PROFILE_ARN Discovery ──────────────────────────────────────────────────

discover_profile_arn() {
    if ! command -v kiro-cli >/dev/null 2>&1; then
        echo ""
        return
    fi
    local output
    output="$(kiro-cli whoami 2>/dev/null)" || { echo ""; return; }
    echo "$output" | sed -n '3p'
}

# ─── .env File Writer ────────────────────────────────────────────────────────

escape_sed() {
    printf '%s' "$1" | sed 's|[&\|]|\\&|g'
}

sed_inplace() {
    local expr="$1" file="$2"
    if [ "$OS_TYPE" = "macos" ]; then
        sed -i '' "$expr" "$file"
    else
        sed -i "$expr" "$file"
    fi
}

write_env() {
    if [ -f ".env" ]; then
        echo "  .env already exists — skipping creation."
        ENV_SKIPPED=true
        return
    fi

    if [ ! -f ".env.example" ]; then
        echo "ERROR: .env.example not found. Run this script from the project root." >&2
        exit 1
    fi

    cp .env.example .env

    # Patch PROXY_API_KEY
    local safe_key safe_creds
    safe_key="$(escape_sed "$PROXY_API_KEY")"
    sed_inplace "s|^PROXY_API_KEY=.*|PROXY_API_KEY=\"${safe_key}\"|" .env

    # Patch KIRO_CREDS_FILE — uncomment and set if discovered
    if [ -n "$CREDS_FILE" ]; then
        safe_creds="$(escape_sed "$CREDS_FILE")"
        sed_inplace "s|^#* *KIRO_CREDS_FILE=.*|KIRO_CREDS_FILE=\"${safe_creds}\"|" .env
    fi

    ENV_WRITTEN=true
    echo "  .env created from .env.example."
}

# ─── Shell RC File Patcher ──────────────────────────────────────────────────

patch_rc_file() {
    if [ ! -w "$RC_FILE" ] && [ ! -e "$RC_FILE" ]; then
        # File doesn't exist yet — we'll create it; check parent dir is writable
        local parent
        parent="$(dirname "$RC_FILE")"
        if [ ! -w "$parent" ]; then
            echo "ERROR: Cannot write to $RC_FILE (permission denied)." >&2
            exit 1
        fi
    elif [ -e "$RC_FILE" ] && [ ! -w "$RC_FILE" ]; then
        echo "ERROR: $RC_FILE exists but is not writable." >&2
        exit 1
    fi

    local need_url=true need_key=true
    grep -q 'ANTHROPIC_BASE_URL' "$RC_FILE" 2>/dev/null && need_url=false
    grep -q 'ANTHROPIC_API_KEY' "$RC_FILE" 2>/dev/null && need_key=false

    if ! $need_url && ! $need_key; then
        echo "  $RC_FILE already contains ANTHROPIC exports — skipping."
        RC_SKIPPED=true
        return
    fi

    {
        echo ""
        echo "# Added by go-kiro-gateway setup.sh"
        if $need_url; then echo "export ANTHROPIC_BASE_URL=\"http://localhost:8000\""; fi
        if $need_key; then echo "export ANTHROPIC_API_KEY=\"${PROXY_API_KEY}\""; fi
        echo "# End go-kiro-gateway setup"
    } >> "$RC_FILE"

    RC_PATCHED=true
    echo "  Exports appended to $RC_FILE."
}

# ─── Summary & Main ──────────────────────────────────────────────────────────

print_summary() {
    echo ""
    echo "========================================"
    echo " go-kiro-gateway setup summary"
    echo "========================================"

    if [ -n "$CREDS_FILE" ]; then
        echo "  Credentials : $CREDS_FILE"
    else
        echo "  Credentials : NOT FOUND — set KIRO_CREDS_FILE in .env manually"
    fi

    if $ENV_WRITTEN; then
        echo "  .env        : created"
    elif $ENV_SKIPPED; then
        echo "  .env        : already exists (not modified)"
    fi

    if [ -n "$PROFILE_ARN" ]; then
        echo "  PROFILE_ARN : $PROFILE_ARN"
    else
        echo "  PROFILE_ARN : NOT SET — fill in .env manually"
    fi

    echo "  PROXY_API_KEY: $PROXY_API_KEY"
    echo "  (record this — it is your gateway API key)"

    if $RC_PATCHED; then
        echo "  Shell rc    : exports added to $RC_FILE"
    elif $RC_SKIPPED; then
        echo "  Shell rc    : $RC_FILE already up to date"
    fi

    echo ""
    echo "  Next: source $RC_FILE  (or open a new terminal)"
    echo "========================================"
}

main() {
    echo "go-kiro-gateway setup"
    echo ""

    detect_os
    echo "  OS: $OS_TYPE"

    select_rc_file
    echo "  Shell rc file: $RC_FILE"

    echo ""
    echo "Discovering credentials..."
    discover_credentials

    echo ""
    echo "Generating PROXY_API_KEY..."
    PROXY_API_KEY="$(generate_api_key)"
    echo "  Generated."

    echo ""
    echo "Writing .env..."
    write_env

    if ! $ENV_SKIPPED; then
        echo ""
        echo "Discovering PROFILE_ARN..."
        PROFILE_ARN="$(discover_profile_arn)" || true
        if [ -n "$PROFILE_ARN" ]; then
            echo "  Found: $PROFILE_ARN"
            local safe_arn
            safe_arn="$(escape_sed "$PROFILE_ARN")"
            sed_inplace "s|^PROFILE_ARN=.*|PROFILE_ARN=\"${safe_arn}\"|" .env
        else
            echo "  Not found via kiro-cli. Fill in PROFILE_ARN in .env manually."
            echo "  Run: kiro-cli whoami  (line 3 is your profile ARN)"
        fi
    fi

    echo ""
    echo "Patching shell rc file..."
    patch_rc_file

    print_summary
}

main
