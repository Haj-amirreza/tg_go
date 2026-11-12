package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Haj-amirreza/tg_go/backend/internal/botops"
	"github.com/Haj-amirreza/tg_go/backend/internal/bundled"
	"github.com/Haj-amirreza/tg_go/backend/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
)

func operatorMode(mode string) bool {
	switch mode {
	case "import-bundled", "bot-status", "bot-prepare", "bot-enable", "bot-disable", "admin-link", "admin-add-telegram":
		return true
	}
	return false
}
func runOperation(ctx context.Context, pool *pgxpool.Pool, c config.Config, mode string, input io.Reader, output io.Writer) error {
	encode := func(v any) error { return json.NewEncoder(output).Encode(v) }
	if mode == "import-bundled" {
		report, err := bundled.Run(ctx, pool, c.AppKey, bundled.Paths{Contacts: "/database/seed/directory-safe.jsonl.gz", Books: "/database/seed/university-books.jsonl.gz", RawContacts: "/database/seed/bkatu_contacts_full.json"})
		if err != nil {
			return err
		}
		return encode(report)
	}
	options := botops.Options{RequireBundleImport: true}
	if mode == "bot-status" {
		s, err := botops.Check(ctx, pool, options)
		if err != nil {
			return err
		}
		return encode(s)
	}
	if mode == "bot-disable" {
		if err := botops.Disable(ctx, pool); err != nil {
			return err
		}
		return encode(map[string]bool{"disabled": true})
	}
	var in struct {
		ID           int64  `json:"id"`
		Username     string `json:"username"`
		AllowPending bool   `json:"allow_pending_updates"`
		TelegramID   string `json:"telegram_id"`
	}
	decoder := json.NewDecoder(io.LimitReader(input, 8192))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid operator input")
	}
	if mode == "admin-link" {
		if err := botops.BindAdmin(ctx, pool, in.Username, in.TelegramID); err != nil {
			return err
		}
		return encode(map[string]bool{"linked": true})
	}
	if mode == "admin-add-telegram" {
		if err := botops.AddTelegramAdmin(ctx, pool, in.TelegramID); err != nil {
			return err
		}
		return encode(map[string]bool{"linked": true})
	}
	options.AllowPendingUpdates = in.AllowPending
	metadata := botops.Metadata{ID: in.ID, Username: in.Username}
	var s botops.Status
	var err error
	if mode == "bot-prepare" {
		s, err = botops.Prepare(ctx, pool, metadata, options)
	} else {
		s, err = botops.Enable(ctx, pool, metadata, options)
	}
	if err != nil {
		return err
	}
	return encode(s)
}
