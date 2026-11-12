package identity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

type BootstrapInput struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	Name       string `json:"name"`
	TelegramID string `json:"telegram_id"`
}

// Bootstrap is an operator-only, empty-database operation; there is no HTTP equivalent.
func (s *Service) Bootstrap(ctx context.Context, in BootstrapInput) error {
	if !usernamePattern.MatchString(in.Username) || len(in.Password) < 12 || len(in.Password) > 72 || strings.TrimSpace(in.Name) == "" || utf8.RuneCountInString(in.Name) > 120 || (in.TelegramID != "" && !telegramID.MatchString(in.TelegramID)) {
		return errors.New("invalid bootstrap input")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(in.Password), 12)
	if err != nil {
		return ErrUnavailable
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ErrUnavailable
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(764629183)"); err != nil {
		return ErrUnavailable
	}
	var count int
	if tx.QueryRow(ctx, "SELECT COUNT(*) FROM admins").Scan(&count) != nil {
		return ErrUnavailable
	}
	if count != 0 {
		return errors.New("bootstrap requires an empty admin registry")
	}
	raw, _ := json.Marshal(Permissions)
	var role, id int64
	if err = tx.QueryRow(ctx, "INSERT INTO roles(name,permissions,level) VALUES($1,$2,1) ON CONFLICT(name) DO UPDATE SET permissions=EXCLUDED.permissions,level=1 RETURNING id", "مدیرکل", string(raw)).Scan(&role); err != nil {
		return ErrUnavailable
	}
	if err = tx.QueryRow(ctx, "INSERT INTO admins(username,password_hash,name,role_id,is_super,created_at) VALUES($1,$2,$3,$4,true,$5) RETURNING id", in.Username, string(h), in.Name, role, s.now().Unix()).Scan(&id); err != nil {
		return ErrUnavailable
	}
	if in.TelegramID != "" {
		if _, err = tx.Exec(ctx, "INSERT INTO admin_telegram_links(admin_id,telegram_id,linked_at) VALUES($1,$2,$3)", id, in.TelegramID, s.now().Unix()); err != nil {
			return ErrUnavailable
		}
	}
	if _, err = tx.Exec(ctx, "INSERT INTO identity_audit(admin_id,action,created_at) VALUES($1,'bootstrap',$2)", id, s.now().Unix()); err != nil {
		return ErrUnavailable
	}
	if tx.Commit(ctx) != nil {
		return ErrUnavailable
	}
	return nil
}
