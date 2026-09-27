#!/usr/bin/env bash
# Generates the local-dev secrets that don't need to be shared across the
# team — they only have to be random and consistent on one machine:
#
#   backend .env:        SESSION_KEY, JWT_PRIVATE_KEY + JWT_PUBLIC_KEY (RS256
#                        pair), MCP_SERVICE_SECRET, ONBOARDING_SERVICE_SECRET
#                        (a pair under the old names JWTSigningKey /
#                        JWTValidatingKey, which the backend no longer
#                        reads, is moved to the new names)
#   frontend .env.local: JWT_PUBLIC_KEY (the backend's public key, read by
#                        src/proxy.ts), JWT_SECRET
#
# It also turns on DEVELOPMENT_MODE in the backend .env when it's unset,
# since .env.example ships it off. Both files are made readable only by you.
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
umask 077

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

# get_var FILE KEY — prints the raw value of the last uncommented KEY= line
# (dotenv loaders let a later duplicate win), quotes and comment included.
get_var() {
  awk -v k="$2" 'index($0, k "=") == 1 { v = substr($0, length(k) + 2); found = 1 }
    END { if (found) print v }' "$1"
}

# value_of FILE KEY — KEY's value as a dotenv loader sees it: the text
# inside matching quotes, or an unquoted value up to a " #" comment.
value_of() {
  local v
  v="$(get_var "$1" "$2")"
  v="${v#"${v%%[![:space:]]*}"}"   # trim leading whitespace
  case "$v" in
    \"*) v="${v#\"}"; v="${v%%\"*}" ;;
    \'*) v="${v#\'}"; v="${v%%\'*}" ;;
    *) v="${v%%[[:space:]]#*}"; v="${v%"${v##*[![:space:]]}"}" ;;
  esac
  printf '%s' "$v"
}

# needs_value FILE KEY — true if KEY is missing, empty, or a <PLACEHOLDER>.
needs_value() {
  $force && return 0
  local v
  v="$(value_of "$1" "$2")"
  [[ -z "$v" || "$v" == \<*\> ]]
}

# set_var FILE KEY VALUE — replaces the first KEY= line and drops any later
# duplicates (which would otherwise win when the file is loaded), or
# appends one.
set_var() {
  local tmp
  tmp="$(mktemp)"
  K="$2" V="$3" awk '
    index($0, ENVIRON["K"] "=") == 1 { if (!done) { print ENVIRON["K"] "=" ENVIRON["V"]; done = 1 }; next }
    { print }
    END { if (!done) print ENVIRON["K"] "=" ENVIRON["V"] }
  ' "$1" > "$tmp"
  cat "$tmp" > "$1"
  rm -f "$tmp"
  echo "  set $2"
}

# unset_var FILE KEY — removes every KEY= line.
unset_var() {
  local tmp
  tmp="$(mktemp)"
  K="$2" awk 'index($0, ENVIRON["K"] "=") != 1' "$1" > "$tmp"
  cat "$tmp" > "$1"
  rm -f "$tmp"
}

# rename_var FILE OLD NEW — moves OLD's value to NEW if OLD has a real value
# and NEW is missing, empty, or a <PLACEHOLDER>, then removes every OLD=
# line. Under --force nothing is moved, since the value is regenerated.
rename_var() {
  if ! needs_value "$1" "$2" && needs_value "$1" "$3"; then
    set_var "$1" "$3" "$(get_var "$1" "$2")"
  fi
  if grep -q "^$2=" "$1"; then
    unset_var "$1" "$2"
    echo "  removed old $2"
  fi
}

# ensure_env_file FILE EXAMPLE — creates FILE from EXAMPLE if it's missing,
# and makes it readable only by you either way, since it holds private keys.
# Returns 0 if it had to create the file.
ensure_env_file() {
  local created=1
  if [[ ! -f "$1" ]]; then
    cp "$2" "$1"
    echo "created $1 from $(basename "$2")"
    created=0
  fi
  chmod 600 "$1"
  return $created
}

# pem_one_line FILE — a PEM file as a double-quoted, single-line value with
# literal \n separators; godotenv and Next.js both expand \n inside double
# quotes, and src/proxy.ts additionally handles a literal \n.
pem_one_line() {
  printf '"%s"' "$(awk '{ printf "%s\\n", $0 }' "$1")"
}

backend_env=".env"
backend_created=false
if ensure_env_file "$backend_env" ".env.example"; then backend_created=true; fi
echo "backend ($backend_env):"

for key in SESSION_KEY MCP_SERVICE_SECRET ONBOARDING_SERVICE_SECRET; do
  if needs_value "$backend_env" "$key"; then
    set_var "$backend_env" "$key" "$(openssl rand -base64 32)"
  fi
done

# .env.example ships DEVELOPMENT_MODE=false so a copied template never
# enables dev behavior; a fresh local .env needs it on. An existing explicit
# value is left alone.
if $backend_created || [[ -z "$(value_of "$backend_env" DEVELOPMENT_MODE)" ]]; then
  set_var "$backend_env" DEVELOPMENT_MODE true
elif [[ "$(value_of "$backend_env" DEVELOPMENT_MODE | tr '[:upper:]' '[:lower:]')" != "true" ]]; then
  echo "  note: DEVELOPMENT_MODE is not true; seed data and DEV_ROLE_OVERRIDES are off"
fi

rename_var "$backend_env" JWTSigningKey JWT_PRIVATE_KEY
rename_var "$backend_env" JWTValidatingKey JWT_PUBLIC_KEY

new_public_key=""
need_priv=false; need_pub=false
needs_value "$backend_env" JWT_PRIVATE_KEY && need_priv=true
needs_value "$backend_env" JWT_PUBLIC_KEY && need_pub=true
if $need_priv && $need_pub; then
  keydir="$(mktemp -d)"
  trap 'rm -rf "$keydir"' EXIT
  openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "$keydir/private.pem" 2>/dev/null
  openssl pkey -in "$keydir/private.pem" -pubout -out "$keydir/public.pem"
  set_var "$backend_env" JWT_PRIVATE_KEY "$(pem_one_line "$keydir/private.pem")"
  new_public_key="$(pem_one_line "$keydir/public.pem")"
  set_var "$backend_env" JWT_PUBLIC_KEY "$new_public_key"
elif $need_priv || $need_pub; then
  echo "  skipped JWT keys: only one of JWT_PRIVATE_KEY/JWT_PUBLIC_KEY is set;" >&2
  echo "  clear both (or pass --force) to generate a matching pair" >&2
fi

if [[ -d "$frontend_dir" ]]; then
  frontend_env="$frontend_dir/.env.local"
  ensure_env_file "$frontend_env" "$frontend_dir/.env.example" || true
  echo "frontend ($frontend_env):"

  # A freshly generated pair must replace the frontend's key, or
  # src/proxy.ts would reject every login outside `next dev` (which skips
  # auth), e.g. under `next start`.
  rename_var "$frontend_env" JWTValidatingKey JWT_PUBLIC_KEY
  if [[ -n "$new_public_key" ]]; then
    set_var "$frontend_env" JWT_PUBLIC_KEY "$new_public_key"
  elif needs_value "$frontend_env" JWT_PUBLIC_KEY; then
    set_var "$frontend_env" JWT_PUBLIC_KEY "$(get_var "$backend_env" JWT_PUBLIC_KEY)"
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
