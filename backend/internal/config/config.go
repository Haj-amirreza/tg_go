package config

import (
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type Config struct {
	Environment, Address, DatabaseURL, StaticDir string
	AppKey, Origin, BotToken                     string
	ReleaseMode, WebhookSecret, BotUsername      string
	SendEnabled                                  bool
	PoolMax                                      int32
}

func Load() (Config, error) { return Parse(os.Getenv) }
func Parse(get func(string) string) (Config, error) {
	c := Config{Environment: get("APP_ENV"), Address: get("HTTP_ADDR"), DatabaseURL: get("DATABASE_URL"), StaticDir: get("STATIC_DIR"), PoolMax: 8, AppKey: get("APP_KEY"), Origin: get("APP_ORIGIN"), BotToken: get("TELEGRAM_BOT_TOKEN"), ReleaseMode: get("RELEASE_MODE"), WebhookSecret: get("TELEGRAM_WEBHOOK_SECRET"), BotUsername: get("TELEGRAM_BOT_USERNAME"), SendEnabled: get("TELEGRAM_SEND_ENABLED") == "true"}
	if c.Environment == "" {
		c.Environment = "development"
	}
	if c.Environment != "development" && c.Environment != "test" && c.Environment != "production" {
		return c, errors.New("unsupported APP_ENV")
	}
	if c.Address == "" {
		c.Address = "127.0.0.1:8080"
	}
	if _, _, err := net.SplitHostPort(c.Address); err != nil {
		return c, errors.New("invalid HTTP_ADDR")
	}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	if v := get("DB_POOL_MAX"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 32 {
			return c, errors.New("DB_POOL_MAX must be 1..32")
		}
		c.PoolMax = int32(n)
	}
	if c.AppKey != "" {
		key, err := hex.DecodeString(c.AppKey)
		if err != nil || len(key) != 32 {
			return c, errors.New("invalid APP_KEY")
		}
		if c.Origin == "" {
			return c, errors.New("APP_ORIGIN is required for identity")
		}
	}
	if c.Environment == "production" {
		if c.ReleaseMode != "web-only" && c.ReleaseMode != "bot" {
			return c, errors.New("production requires explicit web-only or bot release mode")
		}
		if c.AppKey == "" || c.StaticDir == "" {
			return c, errors.New("production requires APP_KEY and STATIC_DIR")
		}
		u, err := url.Parse(c.Origin)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
			return c, errors.New("production requires valid HTTPS APP_ORIGIN")
		}
		if c.ReleaseMode == "web-only" && (c.BotToken != "" || c.WebhookSecret != "" || c.BotUsername != "" || c.SendEnabled) {
			return c, errors.New("web-only production forbids Telegram delivery configuration")
		}
		if v := get("TELEGRAM_SEND_ENABLED"); v != "" && v != "true" && v != "false" {
			return c, errors.New("TELEGRAM_SEND_ENABLED must be true or false")
		}
		if c.ReleaseMode == "bot" && (!ValidBotToken(c.BotToken) || !ValidBotUsername(c.BotUsername) || !ValidWebhookSecret(c.WebhookSecret)) {
			return c, errors.New("bot production requires valid Telegram token, username and strong webhook secret")
		}
	}
	return c, nil
}

var botTokenPattern = regexp.MustCompile(`^([1-9][0-9]{0,18}):[A-Za-z0-9_-]{30,100}$`)
var botUsernamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{3,31}$`)
var webhookSecretPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{32,256}$`)

func ValidBotToken(v string) bool {
	m := botTokenPattern.FindStringSubmatch(v)
	if m == nil {
		return false
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	return err == nil && id > 0
}

func ValidBotUsername(v string) bool {
	return len(v) >= 5 && len(v) <= 32 && botUsernamePattern.MatchString(v) && strings.HasSuffix(strings.ToLower(v), "bot")
}

// Telegram permits URL-safe ASCII secrets; operators should generate them randomly.
func ValidWebhookSecret(v string) bool {
	if !webhookSecretPattern.MatchString(v) {
		return false
	}
	characters := map[rune]bool{}
	for _, character := range v {
		characters[character] = true
	}
	return len(characters) >= 8
}
