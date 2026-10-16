#!/usr/bin/env bash
# Prepare the throwaway Postgres database used by `go test ./...`.
# Requires CHARTDB_TEST_DATABASE_URL (see environment); creates the database
# when missing and applies every pending migration in migrations/.
set -euo pipefail

: "${CHARTDB_TEST_DATABASE_URL:?set CHARTDB_TEST_DATABASE_URL to a postgres URL, e.g. postgres://user:pass@host:5432/chartdb_test?sslmode=disable}"

export CHARTDB_DATABASE_URL="$CHARTDB_TEST_DATABASE_URL"
go run ./server --migrate-only
