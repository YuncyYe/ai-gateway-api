#!/usr/bin/env bash
#
# Local single-node deployment for ai-gateway-api using SQLite.
#
# Build is an INDEPENDENT step; run it before this script:
#   make                       # produces ./ai-gateway-api
#
# Usage:
#   ./scripts/deploy_local_sqlite.sh [--reinit] [--no-start]
#
#   --reinit    drop and recreate the SQLite database (data will be lost)
#   --no-start  only initialize the database, do not start the server
#
set -euo pipefail

# ---------------------------------------------------------------------------
# Paths
# ---------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${PROJECT_ROOT}"

CONF_DIR="${PROJECT_ROOT}/conf"
CONF_FILE="ai_gateway_api_sqlite.toml"
DDL_FILE="${PROJECT_ROOT}/db_ddl_sqlite.sql"
DATA_DIR="${PROJECT_ROOT}/data"
DB_FILE="${DATA_DIR}/ai_gateway.db"
LOG_DIR="${PROJECT_ROOT}/log"
BIN_FILE="${PROJECT_ROOT}/ai-gateway-api"

REINIT=0
START=1
for arg in "$@"; do
  case "${arg}" in
    --reinit)   REINIT=1 ;;
    --no-start) START=0 ;;
    -h|--help)
      sed -n '3,12p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *) echo "unknown option: ${arg}" >&2; exit 2 ;;
  esac
done

log() { printf '[deploy] %s\n' "$*"; }
die() { printf '[deploy][error] %s\n' "$*" >&2; exit 1; }

# ---------------------------------------------------------------------------
# 0. Resolve binary (build is done separately, e.g. `make`)
# ---------------------------------------------------------------------------
[[ -x "${BIN_FILE}" ]] || die "binary not found: ${BIN_FILE}. Build it first as an independent step (e.g. run 'make')."

# ---------------------------------------------------------------------------
# 1. Initialize SQLite database
# ---------------------------------------------------------------------------
mkdir -p "${DATA_DIR}" "${LOG_DIR}"

if [[ "${REINIT}" -eq 1 ]]; then
  log "reinit requested: removing existing database"
  rm -f "${DB_FILE}" "${DB_FILE}-wal" "${DB_FILE}-shm"
fi

if [[ ! -f "${DB_FILE}" ]]; then
  [[ -f "${DDL_FILE}" ]] || die "DDL file not found: ${DDL_FILE}"
  log "initializing SQLite database: ${DB_FILE}"
  if command -v sqlite3 >/dev/null 2>&1; then
    sqlite3 "${DB_FILE}" < "${DDL_FILE}"
  elif command -v python3 >/dev/null 2>&1; then
    python3 - "$DB_FILE" "$DDL_FILE" <<'PY'
import sqlite3, sys
db, ddl = sys.argv[1], sys.argv[2]
with open(ddl, "r", encoding="utf-8") as f:
    script = f.read()
conn = sqlite3.connect(db)
conn.executescript(script)
conn.commit()
conn.close()
PY
  else
    die "neither sqlite3 nor python3 is available to apply ${DDL_FILE}"
  fi
  log "database initialized (default login: admin / admin)"
else
  log "database already exists: ${DB_FILE} (use --reinit to recreate)"
fi

# ---------------------------------------------------------------------------
# 2. Start server
# ---------------------------------------------------------------------------
if [[ "${START}" -eq 0 ]]; then
  log "database ready; skipping server start (--no-start)"
  exit 0
fi

log "starting ai-gateway-api ..."
log "  API    : http://localhost:8183"
log "  Monitor: http://localhost:8284"
log "  login  : admin / admin"
exec "${BIN_FILE}" -c "${CONF_DIR}" -sc "${CONF_FILE}" -l "${LOG_DIR}"