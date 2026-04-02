#!/bin/sh
set -eu

DATA_DIR="${PGDATA:-/var/lib/postgresql/data/pgdata}"
if [ "$DATA_DIR" = "/var/lib/postgresql/data" ]; then
  DATA_DIR="/var/lib/postgresql/data/pgdata"
fi
POSTGRES_DB="${POSTGRES_DB:-sharepier}"
POSTGRES_USER="${POSTGRES_USER:-sharepier}"
POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-sharepier}"

if ! id postgres >/dev/null 2>&1; then
  addgroup -S postgres >/dev/null 2>&1 || true
  adduser -S -D -h /var/lib/postgresql -G postgres postgres >/dev/null 2>&1 || true
fi

mkdir -p "$DATA_DIR" /run/postgresql /var/lib/postgresql
chown -R postgres:postgres /run/postgresql /var/lib/postgresql
chmod 700 "$DATA_DIR"

ensure_pg_hba() {
  touch "$DATA_DIR/pg_hba.conf"

  if ! grep -q "^host all all 0.0.0.0/0 scram-sha-256$" "$DATA_DIR/pg_hba.conf"; then
    printf '\nhost all all 0.0.0.0/0 scram-sha-256\n' >>"$DATA_DIR/pg_hba.conf"
  fi

  if ! grep -q "^host all all ::/0 scram-sha-256$" "$DATA_DIR/pg_hba.conf"; then
    printf 'host all all ::/0 scram-sha-256\n' >>"$DATA_DIR/pg_hba.conf"
  fi
}

if [ ! -s "$DATA_DIR/PG_VERSION" ]; then
  password_file="$(mktemp)"
  printf '%s\n' "$POSTGRES_PASSWORD" > "$password_file"
  chown postgres:postgres "$password_file"
  chmod 600 "$password_file"

  su-exec postgres initdb \
    -D "$DATA_DIR" \
    --username=postgres \
    --pwfile="$password_file" \
    --auth-host=scram-sha-256 \
    --auth-local=trust

  rm -f "$password_file"

  cat >>"$DATA_DIR/postgresql.conf" <<'EOF'
listen_addresses='*'
port=5432
EOF

  ensure_pg_hba

  su-exec postgres pg_ctl -D "$DATA_DIR" -w start

  if [ "$POSTGRES_USER" = "postgres" ]; then
    su-exec postgres psql --username=postgres -v ON_ERROR_STOP=1 <<EOSQL
ALTER USER postgres WITH PASSWORD '${POSTGRES_PASSWORD}';
SELECT 'CREATE DATABASE "${POSTGRES_DB}" OWNER postgres'
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = '${POSTGRES_DB}')\gexec
EOSQL
  else
    su-exec postgres psql --username=postgres -v ON_ERROR_STOP=1 <<EOSQL
DO \$\$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '${POSTGRES_USER}') THEN
    CREATE ROLE "${POSTGRES_USER}" LOGIN PASSWORD '${POSTGRES_PASSWORD}';
  ELSE
    ALTER ROLE "${POSTGRES_USER}" WITH LOGIN PASSWORD '${POSTGRES_PASSWORD}';
  END IF;
END
\$\$;
SELECT 'CREATE DATABASE "${POSTGRES_DB}" OWNER "${POSTGRES_USER}"'
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = '${POSTGRES_DB}')\gexec
EOSQL
  fi

  su-exec postgres pg_ctl -D "$DATA_DIR" -m fast -w stop
fi

ensure_pg_hba
exec su-exec postgres postgres -D "$DATA_DIR"
