CREATE TABLE work_jobs (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 dedupe_key varchar(200) NOT NULL UNIQUE,
 kind varchar(80) NOT NULL,
 payload jsonb NOT NULL,
 stream_key varchar(100) NOT NULL DEFAULT '',
 external_effect boolean NOT NULL DEFAULT false,
 priority smallint NOT NULL DEFAULT 0,
 state varchar(20) NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','processing','completed','dead','uncertain')),
 attempts integer NOT NULL DEFAULT 0,
 max_attempts integer NOT NULL DEFAULT 5 CHECK(max_attempts BETWEEN 1 AND 20),
 available_at timestamptz NOT NULL DEFAULT now(),
 lease_token varchar(64), lease_until timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(), finished_at timestamptz,
 error_code varchar(50)
);
CREATE INDEX work_claim ON work_jobs(kind,priority DESC,available_at,id) WHERE state='pending';
CREATE INDEX work_stream ON work_jobs(stream_key,id) WHERE state IN ('pending','processing','uncertain');
CREATE INDEX work_leases ON work_jobs(lease_until) WHERE state='processing';
CREATE TABLE telegram_updates(update_id bigint PRIMARY KEY CHECK(update_id>=0),received_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE queue_control(id boolean PRIMARY KEY DEFAULT true CHECK(id),paused boolean NOT NULL DEFAULT true);
INSERT INTO queue_control VALUES(true,true);
DO $$ BEGIN IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='unibot_app') THEN
 GRANT SELECT,INSERT,UPDATE,DELETE ON work_jobs,telegram_updates TO unibot_app;
 GRANT USAGE ON SEQUENCE work_jobs_id_seq TO unibot_app;
 GRANT SELECT,UPDATE ON queue_control TO unibot_app;
END IF; END $$;
UPDATE runtime_metadata SET value='{"stage":4,"business_schema_complete":false}'::jsonb,updated_at=now() WHERE name='migration_stage';
ALTER TABLE identity_audit ADD COLUMN target varchar(64) NOT NULL DEFAULT '';
