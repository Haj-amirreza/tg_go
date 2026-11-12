package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Haj-amirreza/tg_go/backend/internal/cache"
	"github.com/Haj-amirreza/tg_go/backend/internal/legacy"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
	"log/slog"
	"os"
)

var ErrLimited = errors.New("login temporarily limited")
var ErrUnavailable = errors.New("identity service unavailable")

type Service struct {
	cache         *cache.Cache
	pool          *pgxpool.Pool
	crypto        *legacy.Crypto
	key, botToken string
	now           func() time.Time
	kdf           chan struct{}
	dummy         string
}
type account struct {
	Actor
	Hash   string
	Secret *string
	Last   int64
}

const accountSQL = `SELECT a.id,a.username,a.name,r.name,r.level,a.is_super,r.permissions,a.auth_version,a.password_hash,a.totp_secret,a.totp_last FROM admins a JOIN roles r ON r.id=a.role_id WHERE a.active=true AND `

func scanAccount(row pgx.Row) (account, error) {
	var a account
	var raw string
	err := row.Scan(&a.ID, &a.Username, &a.Name, &a.Role, &a.Level, &a.Super, &raw, &a.Version, &a.Hash, &a.Secret, &a.Last)
	if err != nil {
		return a, err
	}
	if json.Unmarshal([]byte(raw), &a.Permissions) != nil {
		return a, errCredentials
	}
	if a.Super {
		a.Level = 1
		a.Permissions = append([]string{}, Permissions...)
	}
	return a, nil
}
func New(pool *pgxpool.Pool, key, botToken string) (*Service, error) {
	c, err := legacy.New(key)
	if err != nil {
		return nil, err
	}
	dummy, err := bcrypt.GenerateFromPassword([]byte("synthetic-unusable-login-sentinel"), 12)
	if err != nil {
		return nil, err
	}
	store, err := cache.New(64, 1<<20, os.Getenv("PUBLIC_CACHE_DIR"))
	if err != nil {
		slog.Warn("persistent public cache unavailable; using memory cache")
		store, _ = cache.New(64, 1<<20, "")
	}
	return &Service{cache: store, pool: pool, crypto: c, key: key, botToken: botToken, now: time.Now, kdf: make(chan struct{}, 2), dummy: string(dummy)}, nil
}
func hash(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", ErrUnavailable
	}
	return hex.EncodeToString(b), nil
}
func (s *Service) mac(value string) string {
	h := hmac.New(sha256.New, []byte(s.key))
	h.Write([]byte(value))
	return hex.EncodeToString(h.Sum(nil))
}
func (s *Service) csrf(token string) string { return s.mac("csrf:" + token) }
func (s *Service) rate(ctx context.Context, ip, user string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, ErrUnavailable
	}
	defer tx.Rollback(context.Background())
	now := s.now().Unix()
	allowed := true
	for _, item := range []struct {
		key string
		max int
	}{{"ip:" + ip, 15}, {"user:" + strings.ToLower(user), 5}} {
		var hits int
		err = tx.QueryRow(ctx, `INSERT INTO auth_attempts(bucket,hits,expires_at) VALUES($1,1,$2) ON CONFLICT(bucket) DO UPDATE SET hits=CASE WHEN auth_attempts.expires_at<=$3 THEN 1 ELSE least(auth_attempts.hits+1,10000) END,expires_at=CASE WHEN auth_attempts.expires_at<=$3 THEN $2 ELSE auth_attempts.expires_at END RETURNING hits`, s.mac(item.key), now+900, now).Scan(&hits)
		if err != nil {
			return false, ErrUnavailable
		}
		allowed = allowed && hits <= item.max
	}
	if tx.Commit(ctx) != nil {
		return false, ErrUnavailable
	}
	return allowed, nil
}
func (s *Service) otp(ctx context.Context, tx pgx.Tx, a account, code string) error {
	if a.Secret == nil || *a.Secret == "" {
		return nil
	}
	plain, err := s.crypto.Decrypt(*a.Secret)
	if err != nil {
		return errCredentials
	}
	counter, err := verifyTOTP(string(plain), code, a.Last, s.now())
	if err != nil {
		return errCredentials
	}
	tag, err := tx.Exec(ctx, "UPDATE admins SET totp_last=$1 WHERE id=$2 AND totp_last<$1", counter, a.ID)
	if err != nil {
		return ErrUnavailable
	}
	if tag.RowsAffected() != 1 {
		return errCredentials
	}
	return nil
}
func (s *Service) session(ctx context.Context, tx pgx.Tx, a account, telegram string) (string, Actor, error) {
	token, err := randomToken()
	if err != nil {
		return "", Actor{}, err
	}
	now := s.now().Unix()
	life := int64(3600)
	if telegram != "" {
		life = 1800
	}
	if _, err = tx.Exec(ctx, "INSERT INTO admin_sessions(token_hash,admin_id,auth_version,expires_at,created_at,telegram_id) VALUES($1,$2,$3,$4,$5,NULLIF($6,''))", hash(token), a.ID, a.Version, now+life, now, telegram); err != nil {
		return "", Actor{}, ErrUnavailable
	}
	if _, err = tx.Exec(ctx, "INSERT INTO identity_audit(admin_id,action,created_at) VALUES($1,$2,$3)", a.ID, "login", now); err != nil {
		return "", Actor{}, ErrUnavailable
	}
	a.CSRF = s.csrf(token)
	return token, a.Actor, nil
}
func (s *Service) Login(ctx context.Context, username, password, code, ip string) (string, Actor, error) {
	username = strings.TrimSpace(username)
	if !usernamePattern.MatchString(username) || len(password) > 72 {
		return "", Actor{}, errCredentials
	}
	allowed, err := s.rate(ctx, ip, username)
	if err != nil {
		return "", Actor{}, err
	}
	if !allowed {
		return "", Actor{}, ErrLimited
	}
	select {
	case s.kdf <- struct{}{}:
		defer func() { <-s.kdf }()
	default:
		return "", Actor{}, ErrLimited
	}
	a, err := scanAccount(s.pool.QueryRow(ctx, accountSQL+"lower(a.username)=lower($1)", username))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", Actor{}, ErrUnavailable
	}
	candidate := a.Hash
	if err != nil {
		candidate = s.dummy
	}
	valid := passwordOK(candidate, password)
	if !valid || err != nil {
		return "", Actor{}, errCredentials
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", Actor{}, ErrUnavailable
	}
	defer tx.Rollback(context.Background())
	fresh, err := scanAccount(tx.QueryRow(ctx, accountSQL+"a.id=$1 FOR UPDATE OF a", a.ID))
	if err != nil || fresh.Version != a.Version || fresh.Hash != a.Hash {
		return "", Actor{}, errCredentials
	}
	if err = s.otp(ctx, tx, fresh, code); err != nil {
		return "", Actor{}, err
	}
	token, actor, err := s.session(ctx, tx, fresh, "")
	if err != nil {
		return "", Actor{}, err
	}
	if tx.Commit(ctx) != nil {
		return "", Actor{}, ErrUnavailable
	}
	return token, actor, nil
}
func (s *Service) Current(ctx context.Context, token string) (Actor, error) {
	if len(token) != 64 {
		return Actor{}, errCredentials
	}
	// The linked Telegram ID is revalidated for every Mini App session request.
	row := s.pool.QueryRow(ctx, `SELECT a.id,a.username,a.name,r.name,r.level,a.is_super,r.permissions,a.auth_version,a.password_hash,a.totp_secret,a.totp_last FROM admin_sessions ss JOIN admins a ON a.id=ss.admin_id JOIN roles r ON r.id=a.role_id WHERE ss.token_hash=$1 AND ss.expires_at>$2 AND a.active=true AND a.auth_version=ss.auth_version AND (ss.telegram_id IS NULL OR EXISTS(SELECT 1 FROM admin_telegram_links l WHERE l.admin_id=a.id AND l.telegram_id=ss.telegram_id))`, hash(token), s.now().Unix())
	a, err := scanAccount(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Actor{}, errCredentials
		}
		return Actor{}, ErrUnavailable
	}
	a.CSRF = s.csrf(token)
	return a.Actor, nil
}
func (s *Service) Logout(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM admin_sessions WHERE token_hash=$1", hash(token))
	if err != nil {
		return ErrUnavailable
	}
	return nil
}
func (s *Service) IssueGrant(ctx context.Context, telegram, page string) (string, error) {
	if !telegramID.MatchString(telegram) {
		return "", errCredentials
	}
	a, err := scanAccount(s.pool.QueryRow(ctx, accountSQL+"a.id=(SELECT admin_id FROM admin_telegram_links WHERE telegram_id=$1)", telegram))
	if err != nil || !a.Can(page) {
		return "", errCredentials
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	now := s.now().Unix()
	_, err = s.pool.Exec(ctx, "INSERT INTO admin_access_grants(token_hash,admin_id,telegram_id,auth_version,page,expires_at,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)", hash(token), a.ID, telegram, a.Version, page, now+180, now)
	if err != nil {
		return "", ErrUnavailable
	}
	return token, nil
}
func (s *Service) Telegram(ctx context.Context, grant, data, code, ip string) (string, Actor, error) {
	allowed, err := s.rate(ctx, ip, "mini:"+hash(grant))
	if err != nil {
		return "", Actor{}, err
	}
	if !allowed {
		return "", Actor{}, ErrLimited
	}
	if len(grant) != 64 {
		return "", Actor{}, errCredentials
	}
	identity, err := VerifyTelegram(data, s.botToken, s.now())
	if err != nil {
		return "", Actor{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", Actor{}, ErrUnavailable
	}
	defer tx.Rollback(context.Background())
	var id int64
	var version int
	var telegram, page string
	err = tx.QueryRow(ctx, "SELECT admin_id,auth_version,telegram_id,page FROM admin_access_grants WHERE token_hash=$1 AND expires_at>$2 FOR UPDATE", hash(grant), s.now().Unix()).Scan(&id, &version, &telegram, &page)
	if err != nil || telegram != identity.ID {
		return "", Actor{}, errCredentials
	}
	a, err := scanAccount(tx.QueryRow(ctx, accountSQL+"a.id=$1 AND EXISTS(SELECT 1 FROM admin_telegram_links l WHERE l.admin_id=a.id AND l.telegram_id=$2) FOR UPDATE OF a", id, telegram))
	if err != nil || a.Version != version || !a.Can(page) {
		return "", Actor{}, errCredentials
	}
	if err = s.otp(ctx, tx, a, code); err != nil {
		return "", Actor{}, err
	}
	// A signed launch payload can establish one session only, even with a different grant.
	tag, err := tx.Exec(ctx, "INSERT INTO telegram_login_replays(digest,expires_at) VALUES($1,$2) ON CONFLICT DO NOTHING", identity.Digest, s.now().Unix()+600)
	if err != nil {
		return "", Actor{}, ErrUnavailable
	}
	if tag.RowsAffected() != 1 {
		return "", Actor{}, errCredentials
	}
	if _, err = tx.Exec(ctx, "DELETE FROM admin_access_grants WHERE token_hash=$1", hash(grant)); err != nil {
		return "", Actor{}, ErrUnavailable
	}
	token, actor, err := s.session(ctx, tx, a, telegram)
	if err != nil {
		return "", Actor{}, err
	}
	if tx.Commit(ctx) != nil {
		return "", Actor{}, ErrUnavailable
	}
	return token, actor, nil
}

// Sweep is bounded and runs only on explicit scheduler integration in a later stage.
func (s *Service) Sweep(ctx context.Context) error {
	for _, table := range []string{"admin_sessions", "admin_access_grants", "auth_attempts", "telegram_login_replays"} {
		_, err := s.pool.Exec(ctx, "DELETE FROM "+table+" WHERE ctid IN (SELECT ctid FROM "+table+" WHERE expires_at<$1 LIMIT 500)", s.now().Unix())
		if err != nil {
			return ErrUnavailable
		}
	}
	return nil
}
