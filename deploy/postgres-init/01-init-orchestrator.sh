#!/bin/bash
set -e

echo "[init] creating orchestrator database if not exists"

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres <<-EOSQL
    SELECT 'CREATE DATABASE orchestrator'
    WHERE NOT EXISTS (
        SELECT FROM pg_database WHERE datname = 'orchestrator'
    )\gexec
EOSQL

echo "[init] applying orchestrator schema"

psql -v ON_ERROR_STOP=1 \
    --username "$POSTGRES_USER" \
    --dbname orchestrator \
    -f /migrations/001_init.sql

echo "[init] orchestrator database initialized"