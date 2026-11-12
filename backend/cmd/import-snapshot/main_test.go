package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRequiresExplicitApplyBeforeConnection(t *testing.T) {
	var output, errors bytes.Buffer
	t.Setenv("TEST_DATABASE_URL", "postgres://private-user:private-password@invalid.example/production")
	status := run([]string{"-snapshot", "/private/snapshot", "-schema", "rehearsal"}, &output, &errors)
	if status != 2 || output.Len() != 0 || !strings.Contains(errors.String(), "No import performed") || strings.Contains(errors.String(), "private-password") {
		t.Fatal("missing -apply was not rejected safely")
	}
}
func TestTargetConfigRejectsLiveDatabaseAndUnsafeSchema(t *testing.T) {
	for _, test := range []struct{ dsn, schema string }{
		{"postgres://example/production", "rehearsal"},
		{"postgres://example/unibot_test", "public, pg_catalog"},
		{"postgres://example/unibot_test", "pg_catalog"},
		{"postgres://example/unibot_test", "information_schema"},
		{"postgres://example/unibot_test", "rehearsal;drop schema public"},
		{"", "rehearsal"},
	} {
		if _, err := targetConfig(test.dsn, test.schema); err == nil {
			t.Fatal("unsafe destination accepted")
		}
	}
	cfg, err := targetConfig("postgres://example/unibot_test", "rehearsal_1")
	if err != nil || cfg.ConnConfig.RuntimeParams["search_path"] != `"rehearsal_1"` {
		t.Fatal("safe destination rejected")
	}
}
func TestHelpDoesNotReadDatabaseOrMutate(t *testing.T) {
	var output, errors bytes.Buffer
	if status := run([]string{"-help"}, &output, &errors); status != 0 || output.Len() != 0 {
		t.Fatal("help failed")
	}
}

func TestMalformedFlagDoesNotEchoPrivateValues(t *testing.T) {
	var output, errors bytes.Buffer
	status := run([]string{"-timeout=private-password-value"}, &output, &errors)
	if status != 2 || strings.Contains(errors.String(), "private-password-value") || output.Len() != 0 {
		t.Fatal("private argument leaked")
	}
}
