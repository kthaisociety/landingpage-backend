#!/usr/bin/env bash
set -e

cd "$(dirname "$0")"

echo "Starting Postgres and Redis..."
docker compose up -d

echo "Waiting for Postgres to be ready..."
until docker compose exec -T db pg_isready -U postgres -q 2>/dev/null; do
  sleep 1
done

echo "Starting backend..."
go run cmd/api/main.go
