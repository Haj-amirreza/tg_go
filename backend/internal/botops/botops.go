// Package botops provides offline operator transitions for Telegram activation.
// Network identity and webhook verification are the deployment wrapper's responsibility.
package botops

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"time"

	"github.com/Haj-amirreza/tg_go/backend/internal/bundled"
	"github.com/Haj-amirreza/tg_go/backend/internal/config"
	"github.com/Haj-amirreza/tg_go/backend/internal/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// AddTelegramAdmin creates a separate full administrator with an unshared random
// password. Only an offline owner with a previously linked superadmin can call it.
// Existing accounts and identity assignments are never upgraded or reassigned.
func AddTelegramAdmin(ctx context.Context, pool *pgxpool.Pool, telegramID string) error {
	id, err := strconv.ParseInt(telegramID, 10, 64)
	if err != nil || id <= 0 || !telegramIDPattern.MatchString(telegramID) {
		return ErrAdminLink
	}
	tx, err := begin(ctx, pool)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(764629183)`); err != nil {
		return ErrUnavailable
	}
	var paused bool
	var state string
	if tx.QueryRow(ctx, `SELECT paused FROM queue_control WHERE id=true FOR UPDATE`).Scan(&paused) != nil || tx.QueryRow(ctx, `SELECT COALESCE((SELECT value FROM settings WHERE name='bot_state'),'"off"')`).Scan(&state) != nil {
		return ErrUnavailable
	}
	if !paused || state != `"off"` {
		return ErrAdminLink
	}
	var existing bool
	err = tx.QueryRow(ctx, `SELECT a.active AND a.is_super FROM admin_telegram_links l JOIN admins a ON a.id=l.admin_id WHERE l.telegram_id=$1`, telegramID).Scan(&existing)
	if err == nil {
		if existing {
			return nil
		}
		return ErrAdminLink
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ErrUnavailable
	}
	var role int64
	if tx.QueryRow(ctx, `SELECT a.role_id FROM admins a JOIN admin_telegram_links l ON l.admin_id=a.id WHERE a.active AND a.is_super AND l.telegram_id IS NOT NULL ORDER BY a.id LIMIT 1`).Scan(&role) != nil {
		return ErrAdminLink
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		return ErrUnavailable
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(hex.EncodeToString(secret)), 12)
	if err != nil {
		return ErrUnavailable
	}
	var admin int64
	if tx.QueryRow(ctx, `INSERT INTO admins(username,password_hash,name,role_id,is_super,created_at) VALUES($1,$2,$3,$4,true,$5) RETURNING id`, "tg_"+telegramID, string(hash), "مدیر تلگرام", role, time.Now().Unix()).Scan(&admin) != nil {
		return ErrAdminLink
	}
	if _, err = tx.Exec(ctx, `INSERT INTO admin_telegram_links(admin_id,telegram_id,linked_at) VALUES($1,$2,$3)`, admin, telegramID, time.Now().Unix()); err != nil {
		return ErrAdminLink
	}
	if _, err = tx.Exec(ctx, `INSERT INTO identity_audit(admin_id,action,created_at,target) VALUES($1,'offline_telegram_admin_add',$2,$3)`, admin, time.Now().Unix(), "admin:"+strconv.FormatInt(admin, 10)); err != nil {
		return ErrUnavailable
	}
	if tx.Commit(ctx) != nil {
		return ErrUnavailable
	}
	return nil
}

var (
	ErrUnavailable = errors.New("bot operation unavailable")
	ErrMetadata    = errors.New("invalid bot metadata")
	ErrNotReady    = errors.New("bot activation prerequisites not satisfied")
	ErrIdentity    = errors.New("bot identity does not match prepared metadata")
	ErrAdminLink   = errors.New("admin link requires an active superadmin, unused Telegram identity and disabled bot")
)

// The bundle importer uses the same lock, so a partial import cannot be activated.
const importLock int64 = 764629191

type Metadata struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type Options struct {
	RequireBundleImport bool
	// Preserves incoming Telegram updates forwarded while the queue was paused.
	// Pending outbox jobs, processing jobs and uncertain work always block activation.
	AllowPendingUpdates bool
}

type Status struct {
	Ready             bool     `json:"ready"`
	Enabled           bool     `json:"enabled"`
	Paused            bool     `json:"paused"`
	BotID             int64    `json:"bot_id"`
	Username          string   `json:"username"`
	ImportReady       bool     `json:"import_ready"`
	WebAdminReady     bool     `json:"web_admin_ready"`
	AdminReady        bool     `json:"admin_ready"`
	PendingJobs       int64    `json:"pending_jobs"`
	PendingUpdates    int64    `json:"pending_updates"`
	PendingDeliveries int64    `json:"pending_deliveries"`
	ProcessingJobs    int64    `json:"processing_jobs"`
	UncertainJobs     int64    `json:"uncertain_jobs"`
	Blockers          []string `json:"blockers"`
}

func validMetadata(m Metadata) bool { return m.ID > 0 && config.ValidBotUsername(m.Username) }

func begin(ctx context.Context, pool *pgxpool.Pool) (pgx.Tx, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, ErrUnavailable
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, importLock); err != nil {
		tx.Rollback(context.Background())
		return nil, ErrUnavailable
	}
	var owner bool
	if tx.QueryRow(ctx, `SELECT pg_has_role(current_user,c.relowner,'USAGE') FROM pg_class c WHERE c.oid='runtime_metadata'::regclass`).Scan(&owner) != nil || !owner {
		tx.Rollback(context.Background())
		return nil, ErrUnavailable
	}
	if postgres.ReadyTx(ctx, tx) != nil {
		tx.Rollback(context.Background())
		return nil, ErrUnavailable
	}
	return tx, nil
}

func Check(ctx context.Context, pool *pgxpool.Pool, options Options) (Status, error) {
	tx, err := begin(ctx, pool)
	if err != nil {
		return Status{}, err
	}
	defer tx.Rollback(context.Background())
	return status(ctx, tx, options)
}

// Prepare records the getMe identity and atomically closes both delivery gates.
// It does not discard inbox or outbox jobs. Blockers remain visible in the result.
func Prepare(ctx context.Context, pool *pgxpool.Pool, metadata Metadata, options Options) (Status, error) {
	if !validMetadata(metadata) {
		return Status{}, ErrMetadata
	}
	tx, err := begin(ctx, pool)
	if err != nil {
		return Status{}, err
	}
	defer tx.Rollback(context.Background())
	if err = setState(ctx, tx, false, metadata); err != nil {
		return Status{}, err
	}
	s, err := status(ctx, tx, options)
	if err != nil || tx.Commit(ctx) != nil {
		return Status{}, ErrUnavailable
	}
	return s, nil
}

// Enable requires the exact identity prepared after an operator verified getMe.
func Enable(ctx context.Context, pool *pgxpool.Pool, metadata Metadata, options Options) (Status, error) {
	if !validMetadata(metadata) {
		return Status{}, ErrMetadata
	}
	tx, err := begin(ctx, pool)
	if err != nil {
		return Status{}, err
	}
	defer tx.Rollback(context.Background())
	// Prevent ingress, queue claims and leases changing while the readiness decision commits.
	if _, err = tx.Exec(ctx, `LOCK TABLE work_jobs IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return Status{}, ErrUnavailable
	}
	s, err := status(ctx, tx, options)
	if err != nil {
		return Status{}, err
	}
	if s.BotID != metadata.ID || s.Username != metadata.Username {
		return s, ErrIdentity
	}
	if !s.Ready {
		return s, ErrNotReady
	}
	if err = setState(ctx, tx, true, metadata); err != nil {
		return Status{}, err
	}
	if tx.Commit(ctx) != nil {
		return Status{}, ErrUnavailable
	}
	s.Enabled, s.Paused = true, false
	return s, nil
}

// Disable closes the database gates without deleting jobs or changing the webhook.
func Disable(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := begin(ctx, pool)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = setState(ctx, tx, false, Metadata{}); err != nil {
		return err
	}
	if tx.Commit(ctx) != nil {
		return ErrUnavailable
	}
	return nil
}

func setState(ctx context.Context, tx pgx.Tx, enabled bool, metadata Metadata) error {
	tag, err := tx.Exec(ctx, `UPDATE queue_control SET paused=$1 WHERE id=true`, !enabled)
	if err != nil || tag.RowsAffected() != 1 {
		return ErrUnavailable
	}
	value := `"off"`
	if enabled {
		value = `"on"`
	}
	if _, err = tx.Exec(ctx, `INSERT INTO settings(name,value,is_secret,updated_at) VALUES('bot_state',$1,false,$2) ON CONFLICT(name) DO UPDATE SET value=EXCLUDED.value,is_secret=false,updated_at=EXCLUDED.updated_at`, value, time.Now().Unix()); err != nil {
		return ErrUnavailable
	}
	if metadata.ID != 0 {
		raw, _ := json.Marshal(metadata)
		if _, err = tx.Exec(ctx, `INSERT INTO settings(name,value,is_secret,updated_at) VALUES('bot_info',$1,false,$2) ON CONFLICT(name) DO UPDATE SET value=EXCLUDED.value,is_secret=false,updated_at=EXCLUDED.updated_at`, string(raw), time.Now().Unix()); err != nil {
			return ErrUnavailable
		}
	}
	return nil
}

type importMarker struct {
	Version int    `json:"version"`
	Ready   bool   `json:"ready"`
	SHA256  string `json:"source_sha256"`
	Counts  struct {
		Books     int64 `json:"university_books"`
		Contents  int64 `json:"contents"`
		Published int64 `json:"published"`
		Draft     int64 `json:"draft"`
	} `json:"counts"`
}

var sha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func status(ctx context.Context, tx pgx.Tx, options Options) (Status, error) {
	s := Status{Blockers: []string{}}
	if tx.QueryRow(ctx, `SELECT paused FROM queue_control WHERE id=true FOR UPDATE`).Scan(&s.Paused) != nil {
		return s, ErrUnavailable
	}
	rows, err := tx.Query(ctx, `SELECT name,value FROM settings WHERE name IN ('bot_state','bot_info') AND NOT is_secret`)
	if err != nil {
		return s, ErrUnavailable
	}
	var metadata Metadata
	for rows.Next() {
		var name, value string
		if rows.Scan(&name, &value) != nil {
			rows.Close()
			return s, ErrUnavailable
		}
		if name == "bot_info" {
			json.Unmarshal([]byte(value), &metadata)
		} else {
			var state string
			json.Unmarshal([]byte(value), &state)
			s.Enabled = state == "on"
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return s, ErrUnavailable
	}
	if validMetadata(metadata) {
		s.BotID, s.Username = metadata.ID, metadata.Username
	} else {
		s.Blockers = append(s.Blockers, "bot_metadata_required")
	}
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT value FROM runtime_metadata WHERE name='bundled_data_import'`).Scan(&raw)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return s, ErrUnavailable
	}
	var marker importMarker
	if err == nil && json.Unmarshal(raw, &marker) == nil {
		s.ImportReady = marker.Version == 1 && marker.Ready && marker.SHA256 == bundled.SourceSHA256 && marker.Counts.Books == 7991 && marker.Counts.Contents == 670 && marker.Counts.Published == 55 && marker.Counts.Draft == 615
	}
	if options.RequireBundleImport && !s.ImportReady {
		s.Blockers = append(s.Blockers, "bundled_data_import_required")
	}
	if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admins WHERE active AND is_super)`).Scan(&s.WebAdminReady) != nil {
		return s, ErrUnavailable
	}
	if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admins a JOIN admin_telegram_links l ON l.admin_id=a.id WHERE a.active AND a.is_super AND l.telegram_id ~ '^[1-9][0-9]{0,18}$')`).Scan(&s.AdminReady) != nil {
		return s, ErrUnavailable
	}
	if !s.AdminReady {
		s.Blockers = append(s.Blockers, "linked_superadmin_required")
	}
	if tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE state='pending'),count(*) FILTER(WHERE state='pending' AND kind='telegram.update' AND NOT external_effect),count(*) FILTER(WHERE state='pending' AND external_effect),count(*) FILTER(WHERE state='processing'),count(*) FILTER(WHERE state='uncertain') FROM work_jobs`).Scan(&s.PendingJobs, &s.PendingUpdates, &s.PendingDeliveries, &s.ProcessingJobs, &s.UncertainJobs) != nil {
		return s, ErrUnavailable
	}
	allowed := int64(0)
	if options.AllowPendingUpdates {
		allowed = s.PendingUpdates
	}
	if s.PendingJobs > allowed {
		s.Blockers = append(s.Blockers, "pending_jobs_require_review")
	}
	if s.ProcessingJobs > 0 {
		s.Blockers = append(s.Blockers, "processing_jobs_require_review")
	}
	if s.UncertainJobs > 0 {
		s.Blockers = append(s.Blockers, "uncertain_jobs_require_review")
	}
	s.Ready = len(s.Blockers) == 0
	return s, nil
}

var adminUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.@-]{3,80}$`)
var telegramIDPattern = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)

// BindAdmin is an owner-only CLI operation. It never reassigns an existing link.
func BindAdmin(ctx context.Context, pool *pgxpool.Pool, username, telegramID string) error {
	idValue, idError := strconv.ParseInt(telegramID, 10, 64)
	if !adminUsernamePattern.MatchString(username) || !telegramIDPattern.MatchString(telegramID) || idError != nil || idValue <= 0 {
		return ErrAdminLink
	}
	tx, err := begin(ctx, pool)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(764629183)`); err != nil {
		return ErrUnavailable
	}
	var paused bool
	var state string
	if tx.QueryRow(ctx, `SELECT paused FROM queue_control WHERE id=true FOR UPDATE`).Scan(&paused) != nil || tx.QueryRow(ctx, `SELECT COALESCE((SELECT value FROM settings WHERE name='bot_state'),'"off"')`).Scan(&state) != nil {
		return ErrUnavailable
	}
	if !paused || state != `"off"` {
		return ErrAdminLink
	}
	var id int64
	if tx.QueryRow(ctx, `SELECT id FROM admins WHERE lower(username)=lower($1) AND active AND is_super FOR UPDATE`, username).Scan(&id) != nil {
		return ErrAdminLink
	}
	var existing string
	err = tx.QueryRow(ctx, `SELECT COALESCE(telegram_id,'') FROM admin_telegram_links WHERE admin_id=$1 FOR UPDATE`, id).Scan(&existing)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ErrUnavailable
	}
	if existing != "" && existing != telegramID {
		return ErrAdminLink
	}
	var collision bool
	if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admin_telegram_links WHERE telegram_id=$1 AND admin_id<>$2)`, telegramID, id).Scan(&collision) != nil {
		return ErrUnavailable
	}
	if collision {
		return ErrAdminLink
	}
	if _, err = tx.Exec(ctx, `INSERT INTO admin_telegram_links(admin_id,telegram_id,linked_at) VALUES($1,$2,$3) ON CONFLICT(admin_id) DO UPDATE SET telegram_id=EXCLUDED.telegram_id,linked_at=EXCLUDED.linked_at,challenge_hash=NULL,expires_at=NULL`, id, telegramID, time.Now().Unix()); err != nil {
		return ErrAdminLink
	}
	if existing == "" {
		if _, err = tx.Exec(ctx, `INSERT INTO identity_audit(admin_id,action,created_at,target) VALUES($1,'offline_telegram_link',$2,$3)`, id, time.Now().Unix(), "admin:"+strconv.FormatInt(id, 10)); err != nil {
			return ErrUnavailable
		}
	}
	if tx.Commit(ctx) != nil {
		return ErrUnavailable
	}
	return nil
}
