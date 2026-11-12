-- Foundation metadata only. The 58 legacy business tables are not imported here.
CREATE TABLE runtime_metadata (
 name text PRIMARY KEY,
 value jsonb NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO runtime_metadata(name,value) VALUES
 ('migration_stage','{"stage":2,"business_schema_complete":false}'::jsonb);
