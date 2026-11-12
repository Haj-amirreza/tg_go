package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Haj-amirreza/tg_go/backend/internal/bot"
	"github.com/Haj-amirreza/tg_go/backend/internal/cache"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/Haj-amirreza/tg_go/backend/internal/config"
	"github.com/Haj-amirreza/tg_go/backend/internal/identity"
	"github.com/Haj-amirreza/tg_go/backend/internal/platform"
	"github.com/Haj-amirreza/tg_go/backend/internal/postgres"
	"github.com/Haj-amirreza/tg_go/backend/internal/workqueue"
	"github.com/jackc/pgx/v5"
)

func run() error {
	mode := flag.String("mode", "api", "api, healthcheck, worker, sender, scheduler, seed-catalog, migrate, admin-bootstrap, admin-grant, import-bundled, bot-status, bot-prepare, bot-enable, bot-disable, admin-link or admin-add-telegram")
	flag.Parse()
	if *mode == "healthcheck" {
		return checkHealth(os.Getenv("HTTP_ADDR"))
	}
	if *mode != "api" && *mode != "worker" && *mode != "sender" && *mode != "scheduler" && *mode != "seed-catalog" && *mode != "migrate" && *mode != "admin-bootstrap" && *mode != "admin-grant" && !operatorMode(*mode) {
		return errors.New("unknown mode")
	}
	c, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	connect, cancel := context.WithTimeout(ctx, 10*time.Second)
	pool, err := postgres.Open(connect, c.DatabaseURL, c.PoolMax)
	cancel()
	if err != nil {
		return err
	}
	defer pool.Close()
	if operatorMode(*mode) {
		return runOperation(ctx, pool, c, *mode, os.Stdin, os.Stdout)
	}
	if *mode == "migrate" {
		migration, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return postgres.Migrate(migration, pool)
	}
	if *mode == "seed-catalog" {
		file, err := os.Open("database/seed/university-books.jsonl.gz")
		if err != nil {
			return errors.New("catalog seed file unavailable")
		}
		defer file.Close()
		tx, err := pool.Begin(ctx)
		if err != nil {
			return errors.New("seed transaction unavailable")
		}
		defer tx.Rollback(context.Background())
		count, err := catalog.SeedBooks(ctx, tx, file)
		if err != nil {
			return errors.New("catalog seed failed")
		}
		if tx.Commit(ctx) != nil {
			return errors.New("catalog seed commit failed")
		}
		slog.Info("catalog seed completed", "added", count)
		return nil
	}
	var service *identity.Service
	var auth http.Handler
	if c.AppKey != "" {
		service, err = identity.New(pool, c.AppKey, c.BotToken)
		if err != nil {
			return errors.New("identity initialization failed")
		}
		auth, err = identity.NewHTTP(service, c.Origin)
		if err != nil {
			return err
		}
	}
	if *mode == "admin-bootstrap" || *mode == "admin-grant" {
		if service == nil {
			return errors.New("APP_KEY is required")
		}
		decoder := json.NewDecoder(io.LimitReader(os.Stdin, 8192))
		decoder.DisallowUnknownFields()
		if *mode == "admin-bootstrap" {
			var in identity.BootstrapInput
			if decoder.Decode(&in) != nil || decoder.Decode(new(any)) != io.EOF {
				return errors.New("invalid bootstrap input")
			}
			return service.Bootstrap(ctx, in)
		}
		var in struct {
			TelegramID string `json:"telegram_id"`
			Page       string `json:"page"`
		}
		if decoder.Decode(&in) != nil || decoder.Decode(new(any)) != io.EOF {
			return errors.New("invalid grant input")
		}
		token, err := service.IssueGrant(ctx, in.TelegramID, in.Page)
		if err != nil {
			return errors.New("grant not issued")
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"grant": token})
	}
	slog.Info("service started", "mode", *mode, "stage", 4)
	if *mode == "api" {
		base := platform.HandlerWithBot(func(ctx context.Context) error { return postgres.Ready(ctx, pool) }, c.Environment, c.StaticDir, auth, func(ctx context.Context) bool {
			if c.ReleaseMode != "bot" {
				return false
			}
			var enabled bool
			err := pool.QueryRow(ctx, `SELECT NOT q.paused AND COALESCE((SELECT value='"on"' FROM settings WHERE name='bot_state' AND NOT is_secret),false) AND COALESCE((SELECT value::jsonb->>'username'=$1 FROM settings WHERE name='bot_info' AND NOT is_secret),false) FROM queue_control q WHERE id=true`, c.BotUsername).Scan(&enabled)
			return err == nil && enabled
		})
		mux := http.NewServeMux()
		mux.Handle("/", base)
		mux.Handle("/api/v1/telegram/webhook", workqueue.Queue{Pool: pool}.Ingress(os.Getenv("TELEGRAM_WEBHOOK_SECRET")))
		return platform.Serve(ctx, c.Address, mux)
	}
	if *mode == "sender" {
		if os.Getenv("TELEGRAM_SEND_ENABLED") != "true" || c.BotToken == "" {
			return errors.New("explicit TELEGRAM_SEND_ENABLED=true and bot token required")
		}
		q := workqueue.Queue{Pool: pool}
		transport := bot.SecureTransport{Pool: pool, Next: bot.TelegramTransport{Pool: pool, Token: c.BotToken, FileRoot: os.Getenv("PRIVATE_FILE_DIR")}}
		for {
			if ctx.Err() != nil {
				return nil
			}
			worked, err := q.DeliverOne(ctx, transport)
			if err != nil {
				slog.Warn("delivery unavailable")
			}
			delay := 10 * time.Millisecond
			if !worked {
				delay = 250 * time.Millisecond
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
		}
	}
	catalogCache, _ := cache.New(128, 2<<20, "")
	if *mode == "worker" {
		return (workqueue.Queue{Pool: pool}).Run(ctx, map[string]workqueue.InternalHandler{"maintenance.noop": func(context.Context, pgx.Tx, workqueue.Job) error { return nil }, "notice.expand": (bot.Engine{RichEnabled: os.Getenv("TELEGRAM_RICH_ENABLED") == "true", AppKey: c.AppKey, AdminURL: c.Origin + "/manage", Username: os.Getenv("TELEGRAM_BOT_USERNAME")}).ExpandNotice, "telegram.update": (bot.Engine{RichEnabled: os.Getenv("TELEGRAM_RICH_ENABLED") == "true", AppKey: c.AppKey, AdminURL: c.Origin + "/manage", Username: os.Getenv("TELEGRAM_BOT_USERNAME"), PublicCache: catalogCache}).Handle})
	}
	if *mode == "scheduler" {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				job, cancel := context.WithTimeout(ctx, 5*time.Second)
				var err error
				if service != nil {
					err = service.Sweep(job)
				}
				if err == nil {
					err = (workqueue.Queue{Pool: pool}).Recover(job)
				}
				cancel()
				if err != nil {
					slog.Warn("identity cleanup unavailable")
				}
			}
		}
	}
	platform.Wait(ctx)
	return nil
}
func main() {
	if err := run(); err != nil {
		slog.Error("service stopped", "reason", err.Error())
		os.Exit(1)
	}
}
