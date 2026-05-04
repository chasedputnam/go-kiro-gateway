# Code Review Feedback

## Summary

`setup.sh` is well-structured and covers the happy path cleanly. There are two blocking issues: a shell injection risk from unquoted/unescaped variable interpolation into `sed` expressions, and a logic bug where `PROFILE_ARN` is patched in `main()` after `write_env()` has already run but `write_env()` also tries to patch it — causing a double-patch attempt and a misleading warning. Several minor issues around the `set -e` interaction with boolean variables and a redundant `select_rc_file` branch are also noted.

## Findings

### setup.sh

- [x] [BLOCKING] Shell injection risk in `sed_inplace` calls — `CREDS_FILE`, `PROXY_API_KEY`, and `PROFILE_ARN` are interpolated directly into `sed` replacement expressions without escaping
  - Why: If any of these values contain `|`, `\`, `&`, or newlines (e.g. a file path with special chars), the `sed` expression breaks or executes unintended substitutions. `CREDS_FILE` in particular comes from the filesystem and could contain unusual characters.
  - Fix: Escape the replacement string before interpolating. A portable approach is to pre-escape the value:
    ```sh
    escape_sed() { printf '%s' "$1" | sed 's|[&\|/]|\\&|g'; }
    ```
    Then use `$(escape_sed "$CREDS_FILE")` in the sed expression.
  - References: Requirement 6 (safety), Requirement 2

- [x] [BLOCKING] `PROFILE_ARN` is patched twice — once inside `write_env()` (lines 186-191) and again in `main()` (lines 302-307), but `PROFILE_ARN` is always empty when `write_env()` runs because `discover_profile_arn()` is called after it
  - Why: `write_env()` checks `if [ -n "$PROFILE_ARN" ]` but `PROFILE_ARN` is set to `""` at this point, so it always hits the warning branch and prints "PROFILE_ARN not set" even when `kiro-cli` will succeed moments later. The actual patch in `main()` works, but the warning is misleading and the dead code in `write_env()` is confusing.
  - Fix: Remove the `PROFILE_ARN` patch block from `write_env()` entirely (lines 185-191). Keep only the patch in `main()` after `discover_profile_arn()` runs. Move the "not found" warning to `main()` as well.
  - References: Requirement 2

- [x] [SUGGESTION] `set -e` interacts poorly with the boolean variable pattern `$need_url && echo ...` inside the heredoc (lines 226-227)
  - Why: Under `set -e`, a command that exits non-zero aborts the script. `$need_url` expands to `false`, which is a command that exits 1 — this is fine inside `{ } >> file` because the append redirection context suppresses it, but it's fragile and non-obvious. If the context ever changes this could silently skip lines.
  - Fix: Use explicit `if` statements:
    ```sh
    if $need_url; then echo "export ANTHROPIC_BASE_URL=\"http://localhost:8000\""; fi
    if $need_key; then echo "export ANTHROPIC_API_KEY=\"${PROXY_API_KEY}\""; fi
    ```

- [x] [SUGGESTION] `select_rc_file()` has a dead branch — the `elif` and `else` both set `RC_FILE="$HOME/.bash_profile"` (lines 36-40)
  - Why: The `elif [ -f "$HOME/.bash_profile" ]` check is redundant since the `else` does the same thing. This looks like a copy-paste artifact.
  - Fix: Simplify to:
    ```sh
    if [ -f "$HOME/.zshrc" ]; then
        RC_FILE="$HOME/.zshrc"
    else
        RC_FILE="$HOME/.bash_profile"
    fi
    ```

- [x] [SUGGESTION] Section comments reference task numbers (`# ─── Task 1:`, `# ─── Task 2:`) which are implementation artifacts that will rot
  - Why: Task numbers are meaningful during development but meaningless to someone reading the script cold six months from now.
  - Fix: Replace with functional section names, e.g. `# ─── OS Detection ───` and `# ─── Key Generation ───`.

- [x] [NIT] `generate_api_key()` fallback order puts `python3` before `/dev/urandom` + `od`, but `od` is more universally available on minimal Linux systems than `python3`
  - Fix: Swap the order — try `od` before `python3`.

- [x] [NIT] The `grep -o '"expiresAt":"[^"]*"'` pattern (line 84) won't match if the JSON has a space after the colon (`"expiresAt": "..."`)
  - Fix: Use `'"expiresAt" *: *"[^"]*"'` or just `'"expiresAt"'` and extract with `cut`/`awk` more loosely.

## Positive observations

- The `sed_inplace()` helper cleanly abstracts the macOS/Linux `sed -i` difference — good call.
- Conservative expiry handling (treat parse failure as valid) is the right default for a setup script.
- The `is_file_valid()` function is well-decomposed and the `jq`/grep fallback is solid.
- Duplicate-export detection in `patch_rc_file()` correctly handles the case where only one of the two vars is already present.
