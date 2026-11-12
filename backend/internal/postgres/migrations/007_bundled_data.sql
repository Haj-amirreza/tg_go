-- Only the offline owner can read source archives or mark a bundled import complete.
CREATE TABLE bundled_import_runs (
 source_sha256 varchar(64) PRIMARY KEY,
 contact_sha256 varchar(64) NOT NULL,
 book_sha256 varchar(64) NOT NULL,
 report text NOT NULL,
 imported_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE bundled_source_archive (
 source_sha256 varchar(64) PRIMARY KEY REFERENCES bundled_import_runs(source_sha256),
 original_name text NOT NULL,
 plaintext_sha256 varchar(64) NOT NULL,
 payload_enc text NOT NULL
);
REVOKE ALL ON bundled_import_runs,bundled_source_archive FROM PUBLIC;
DO $$ BEGIN IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='unibot_app') THEN
 REVOKE ALL ON bundled_import_runs,bundled_source_archive FROM unibot_app;
END IF; END $$;
