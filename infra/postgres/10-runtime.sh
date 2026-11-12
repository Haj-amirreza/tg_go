#!/bin/sh
(
set -eu
# Development role: metadata reads only. Business-table privileges arrive with their migrations.
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --set=runtime_password="$APP_DB_PASSWORD" <<'SQL'
CREATE ROLE unibot_app LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD :'runtime_password';
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO unibot_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO unibot_app;
SQL

)
