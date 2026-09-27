#!/usr/bin/env bash
# Generates the local-dev secrets that don't need to be shared across the
# team — they only have to be random and consistent on one machine:
#
#   backend .env:        SESSION_KEY, JWTSigningKey + JWTValidatingKey (RS256
#                        pair), MCP_SERVICE_SECRET, ONBOARDING_SERVICE_SECRET
#   frontend .env.local: JWT_PUBLIC_KEY (must match JWTValidatingKey), JWT_SECRET
#
# Only fills values that are missing, empty, or still a <PLACEHOLDER>, so it's
# safe to re-run and never touches shared values like GOOGLE_CLIENT_ID. Pass
# --force to regenerate everything it manages (this logs out every local
# session, since the JWT key pair changes).
#
# Usage:
#   scripts/gen-dev-secrets.sh [--force] [--frontend PATH_TO_FRONTEND_REPO]
#
# The frontend repo defaults to ../landingpage-frontend and is skipped if it
# doesn't exist. Secret values are never printed.
set -euo pipefail

cd "$(dirname "$0")/.."

force=false
frontend_dir="../landingpage-frontend"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --force) force=true; shift ;;
    --frontend) frontend_dir="$2"; shift 2 ;;
    -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 1 ;;
  esac
done

command -v openssl >/dev/null || { echo "openssl is required" >&2; exit 1; }

# get_var FILE KEY — prints the value of an uncommented KEY= line (raw, with
# any surrounding quotes and trailing comment left in place).
get_var() {
  awk -v k="$2" 'index($0, k "=") == 1 { print substr($0, length(k) + 2); exit }' "$1"
}

# needs_value FILE KEY — true if KEY is missing, empty, or a <PLACEHOLDER>.
needs_value() {
  $force && return 0
  local v
  v="$(get_var "$1" "$2")"
  v="${v%%#*}"                           # drop a trailing comment
  v="$(echo "$v" | tr -d '"'"'"' \t')"  # drop quotes and whitespace
  [[ -z "$v" || "$v" == \<*\> ]]
}

# set_var FILE KEY VALUE — replaces the KEY= line in place, or appends one.
set_var() {
  local tmp
  tmp="$(mktemp)"
  K="$2" V="$3" awk '
    index($0, ENVIRON["K"] "=") == 1 && !done { print ENVIRON["K"] "=" ENVIRON["V"]; done = 1; next }
    { print }
    END { if (!done) print ENVIRON["K"] "=" ENVIRON["V"] }
  ' "$1" > "$tmp"
  cat "$tmp" > "$1"
  rm -f "$tmp"
  echo "  set $2"
}

# ensure_env_file FILE EXAMPLE — creates FILE from EXAMPLE if it's missing.
ensure_env_file() {
  if [[ ! -f "$1" ]]; then
    cp "$2" "$1"
    echo "created $1 from $(basename "$2")"
  fi
}

# pem_one_line FILE — a PEM file as a double-quoted, single-line value with
# literal \n separators; godotenv and Next.js both expand \n inside double
# quotes, and src/proxy.ts additionally handles a literal \n.
pem_one_line() {
  printf '"%s"' "$(awk '{ printf "%s\\n", $0 }' "$1")"
}

backend_env=".env"
ensure_env_file "$backend_env" ".env.example"
echo "backend ($backend_env):"

for key in SESSION_KEY MCP_SERVICE_SECRET ONBOARDING_SERVICE_SECRET; do
  if needs_value "$backend_env" "$key"; then
    set_var "$backend_env" "$key" "$(openssl rand -base64 32)"
  fi
done

new_public_key=""
need_priv=false; need_pub=false
needs_value "$backend_env" JWTSigningKey && need_priv=true
needs_value "$backend_env" JWTValidatingKey && need_pub=true
if $need_priv && $need_pub; then
  keydir="$(mktemp -d)"
  trap 'rm -rf "$keydir"' EXIT
  openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "$keydir/private.pem" 2>/dev/null
  openssl pkey -in "$keydir/private.pem" -pubout -out "$keydir/public.pem"
  set_var "$backend_env" JWTSigningKey "$(pem_one_line "$keydir/private.pem")"
  new_public_key="$(pem_one_line "$keydir/public.pem")"
  set_var "$backend_env" JWTValidatingKey "$new_public_key"
elif $need_priv || $need_pub; then
  echo "  skipped JWT keys: only one of JWTSigningKey/JWTValidatingKey is set;" >&2
  echo "  clear both (or pass --force) to generate a matching pair" >&2
fi

if [[ -d "$frontend_dir" ]]; then
  frontend_env="$frontend_dir/.env.local"
  ensure_env_file "$frontend_env" "$frontend_dir/.env.example"
  echo "frontend ($frontend_env):"

  # A freshly generated pair must replace the frontend's key, or every
  # login would be rejected by src/proxy.ts.
  if [[ -n "$new_public_key" ]]; then
    set_var "$frontend_env" JWT_PUBLIC_KEY "$new_public_key"
  elif needs_value "$frontend_env" JWT_PUBLIC_KEY; then
    set_var "$frontend_env" JWT_PUBLIC_KEY "$(get_var "$backend_env" JWTValidatingKey)"
  fi
  if needs_value "$frontend_env" JWT_SECRET; then
    set_var "$frontend_env" JWT_SECRET "$(openssl rand -base64 32)"
  fi
else
  echo "frontend repo not found at $frontend_dir; skipped (use --frontend PATH)"
fi

cat <<'EOF'

Done. If you run kthais-mcp or onboarding-service locally, copy
MCP_SERVICE_SECRET / ONBOARDING_SERVICE_SECRET into their .env files too —
each pair must match. Shared values (e.g. GOOGLE_CLIENT_ID/SECRET for the dev
OAuth client) still come from the team's shared store.
EOF
