package config

import (
	"strings"
	"testing"
)

func TestConfig(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://private:SECRET@db/app"}
	get := func(k string) string { return base[k] }
	c, err := Parse(get)
	if err != nil || c.PoolMax != 8 || c.Environment != "development" {
		t.Fatal("defaults invalid")
	}
	for _, v := range []string{"0", "33", "oops"} {
		base["DB_POOL_MAX"] = v
		if _, err = Parse(get); err == nil {
			t.Fatal("bad pool accepted")
		}
	}
	delete(base, "DB_POOL_MAX")
	base["APP_KEY"] = "bad-key"
	if _, err = Parse(get); err == nil {
		t.Fatal("invalid application key accepted")
	}
	base["APP_KEY"] = strings.Repeat("00", 32)
	if _, err = Parse(get); err == nil {
		t.Fatal("identity origin optional")
	}
	base["APP_ORIGIN"] = "https://example.test"
	if _, err = Parse(get); err != nil {
		t.Fatal("identity configuration rejected")
	}
	base["APP_ENV"] = "production"
	_, err = Parse(get)
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatal("production without opt-in enabled or secret leaked")
	}
	base["RELEASE_MODE"] = "web-only"
	base["STATIC_DIR"] = "/web"
	if _, err = Parse(get); err != nil {
		t.Fatalf("safe web-only production rejected: %v", err)
	}
	base["APP_ORIGIN"] = "http://example.test"
	if _, err = Parse(get); err == nil {
		t.Fatal("production accepted HTTP origin")
	}
	base["APP_ORIGIN"] = "https://example.test"
	base["TELEGRAM_BOT_TOKEN"] = "token-not-real"
	if _, err = Parse(get); err == nil {
		t.Fatal("web-only release accepted Telegram bot activation")
	}
}

func TestProductionBotRequiresCompleteCredentials(t *testing.T) {
	values := map[string]string{"APP_ENV": "production", "RELEASE_MODE": "bot", "DATABASE_URL": "postgres://owner:fixture@db/fixture_test", "APP_KEY": strings.Repeat("1", 64), "APP_ORIGIN": "https://panel.example.org", "STATIC_DIR": "/web", "TELEGRAM_BOT_TOKEN": "123456789:" + strings.Repeat("A", 35), "TELEGRAM_BOT_USERNAME": "university_test_bot", "TELEGRAM_WEBHOOK_SECRET": "0123456789abcdef0123456789abcdef"}
	get := func(k string) string { return values[k] }
	if _, e := Parse(get); e != nil {
		t.Fatal(e)
	}
	for _, key := range []string{"TELEGRAM_BOT_TOKEN", "TELEGRAM_BOT_USERNAME", "TELEGRAM_WEBHOOK_SECRET", "APP_KEY", "STATIC_DIR"} {
		saved := values[key]
		values[key] = ""
		if _, e := Parse(get); e == nil {
			t.Fatal("missing bot prerequisite accepted", key)
		}
		values[key] = saved
	}
	values["TELEGRAM_WEBHOOK_SECRET"] = strings.Repeat("1", 64)
	if _, e := Parse(get); e == nil {
		t.Fatal("weak secret accepted")
	}
	values["RELEASE_MODE"] = "web-only"
	if _, e := Parse(get); e == nil {
		t.Fatal("web-only accepted delivery credentials")
	}
}
