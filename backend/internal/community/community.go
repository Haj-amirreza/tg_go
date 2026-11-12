package community

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/Haj-amirreza/tg_go/backend/internal/support"
	"github.com/jackc/pgx/v5"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type Ad struct {
	ID       int64  `json:"id,string"`
	User     *int64 `json:"userId"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Contact  string `json:"contact"`
	URL      string `json:"url"`
	Place    string `json:"place"`
	Price    *int64 `json:"price"`
	State    string `json:"state"`
	Note     string `json:"note"`
	Revision int    `json:"revision"`
	Expires  int64  `json:"expiresAt"`
	Consent  bool   `json:"consent"`
}

func Table(kind string) string {
	switch kind {
	case "channels":
		return "student_channels"
	case "market":
		return "listings"
	case "lost", "found":
		return "lost_items"
	}
	return ""
}

var channelURL = regexp.MustCompile(`^https://t\.me/(?:[A-Za-z0-9_]{5,32}|\+[A-Za-z0-9_-]+|joinchat/[A-Za-z0-9_-]+)/?$`)

func ChannelURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if regexp.MustCompile(`^@[A-Za-z0-9_]{5,32}$`).MatchString(value) {
		value = "https://t.me/" + value[1:]
	}
	if len(value) > 255 || !channelURL.MatchString(value) {
		return "", catalog.ErrInvalid
	}
	return strings.TrimRight(value, "/"), nil
}
func fields(kind string) string {
	switch kind {
	case "channels":
		return `id,user_id,'channels',title,description,'',url,'',NULL::bigint,status,''::text,revision,0::bigint`
	case "market":
		return `id,user_id,'market',title,body,contact,'','',price,state,moderator_note,revision,expires_at`
	default:
		return `id,user_id,kind,title,body,contact,'',place,NULL::bigint,state,note,revision,0::bigint`
	}
}
func scan(row pgx.Row) (Ad, error) {
	var a Ad
	e := row.Scan(&a.ID, &a.User, &a.Kind, &a.Title, &a.Body, &a.Contact, &a.URL, &a.Place, &a.Price, &a.State, &a.Note, &a.Revision, &a.Expires)
	return a, e
}
func List(ctx context.Context, tx pgx.Tx, kind string, user int64, own, admin bool, state string, page int) ([]Ad, error) {
	table := Table(kind)
	if table == "" || page < 0 || page > 10000 {
		return nil, catalog.ErrInvalid
	}
	public := "state='published'"
	if kind == "channels" {
		public = "status='approved'"
	}
	if kind == "market" {
		public += " AND expires_at>" + strconv.FormatInt(time.Now().Unix(), 10)
	}
	stateField := "state"
	if kind == "channels" {
		stateField = "status"
	}
	query := `SELECT ` + fields(kind) + ` FROM ` + table + ` WHERE ($1 OR ($2 AND user_id=$3) OR (NOT $2 AND ` + public + `)) AND ($4='' OR ` + stateField + `=$4) ORDER BY id DESC LIMIT 11 OFFSET $5`
	rows, e := tx.Query(ctx, query, admin, own, user, state, page*10)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Ad{}
	for rows.Next() {
		a, e := scan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func Get(ctx context.Context, tx pgx.Tx, kind string, id, user int64, admin bool) (Ad, error) {
	table := Table(kind)
	if table == "" {
		return Ad{}, catalog.ErrInvalid
	}
	a, e := scan(tx.QueryRow(ctx, `SELECT `+fields(kind)+` FROM `+table+` WHERE id=$1`, id))
	if e == pgx.ErrNoRows {
		return a, catalog.ErrMissing
	}
	if e != nil {
		return a, e
	}
	if !admin && (a.User == nil || *a.User != user) && !Public(a, time.Now().Unix()) {
		return Ad{}, catalog.ErrMissing
	}
	return a, nil
}
func Public(a Ad, now int64) bool {
	return a.Kind == "channels" && a.State == "approved" || a.Kind != "channels" && a.State == "published" && (a.Expires == 0 || a.Expires > now)
}
func Save(ctx context.Context, tx pgx.Tx, a Ad, user int64, admin bool) (Ad, error) {
	table := Table(a.Kind)
	if table == "" || !catalog.Text(a.Title, 200) || !catalog.Text(a.Body, 5000) || utf8.RuneCountInString(a.Contact) > 300 || utf8.RuneCountInString(a.Place) > 200 {
		return a, catalog.ErrInvalid
	}
	if a.Kind == "channels" {
		var e error
		a.URL, e = ChannelURL(a.URL)
		if e != nil || !catalog.Text(a.Title, 160) || !catalog.Text(a.Body, 500) {
			return a, catalog.ErrInvalid
		}
		a.Contact = ""
	}
	if a.Price != nil && (*a.Price < 0 || *a.Price > 1000000000000) {
		return a, catalog.ErrInvalid
	}
	if !admin {
		if a.Contact != "" && !a.Consent {
			return a, catalog.ErrInvalid
		}
		a.State = "pending"
		a.Note = ""
		a.User = &user
		var approved bool
		if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM student_verifications WHERE user_id=$1 AND status='approved')`, user).Scan(&approved); e != nil {
			return a, e
		}
		if !approved {
			return a, catalog.ErrInvalid
		}
		if e := support.Limit(ctx, tx, fmt.Sprintf("ad:%s:%d", a.Kind, user), 12, 3600); e != nil {
			return a, e
		}
		if a.ID == 0 {
			var count int
			window := time.Now().Unix() - 86400
			where := "created_at>$2"
			maxCount := 3
			if a.Kind == "channels" {
				where = "status='pending' AND created_at>=0*$2"
			}
			e := tx.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE user_id=$1 AND `+where, user, window).Scan(&count)
			if e != nil {
				return a, e
			}
			if count >= maxCount {
				return a, catalog.ErrInvalid
			}
		}
	}
	if a.ID > 0 {
		old, e := Get(ctx, tx, a.Kind, a.ID, user, admin)
		if e != nil {
			return a, e
		}
		if !admin && (old.User == nil || *old.User != user) {
			return a, catalog.ErrMissing
		}
		if old.Revision != a.Revision {
			return a, catalog.ErrConflict
		}
		if admin {
			a.User = old.User
		}
	}
	validState := a.State == "pending" || a.State == "published" || a.State == "rejected" || a.State == "deleted" || a.State == "closed" || a.State == "draft" || a.State == "expired"
	if a.Kind == "channels" {
		validState = a.State == "pending" || a.State == "approved" || a.State == "rejected" || a.State == "deleted"
	}
	if !validState || utf8.RuneCountInString(a.Note) > 1000 {
		return a, catalog.ErrInvalid
	}
	now := time.Now().Unix()
	var e error
	switch a.Kind {
	case "channels":
		if a.ID == 0 {
			e = tx.QueryRow(ctx, `INSERT INTO student_channels(user_id,title,url,description,status,created_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(url) DO NOTHING RETURNING id,revision`, a.User, a.Title, a.URL, a.Body, a.State, now).Scan(&a.ID, &a.Revision)
		} else {
			e = tx.QueryRow(ctx, `UPDATE student_channels SET title=$1,url=$2,description=$3,status=$4,revision=revision+1 WHERE id=$5 AND revision=$6 RETURNING revision`, a.Title, a.URL, a.Body, a.State, a.ID, a.Revision).Scan(&a.Revision)
		}
	case "market":
		if a.User == nil {
			return a, catalog.ErrInvalid
		}
		hash := sha256.Sum256([]byte(strings.ReplaceAll(catalog.Normalize(a.Title+a.Body), " ", "")))
		fingerprint := fmt.Sprintf("%x", hash)
		if !admin {
			var duplicate bool
			e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM listings WHERE user_id=$1 AND fingerprint=$2 AND id<>$3 AND state NOT IN ('deleted','expired','rejected'))`, a.User, fingerprint, a.ID).Scan(&duplicate)
			if e != nil {
				return a, e
			}
			if duplicate {
				return a, catalog.ErrConflict
			}
			a.Expires = now + 7*86400
		}
		if a.Expires < now && a.State == "published" {
			return a, catalog.ErrInvalid
		}
		if a.Expires == 0 {
			a.Expires = now + 7*86400
		}
		if a.ID == 0 {
			e = tx.QueryRow(ctx, `INSERT INTO listings(user_id,title,body,price,contact,state,moderator_note,fingerprint,expires_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10) RETURNING id,revision`, a.User, a.Title, a.Body, a.Price, a.Contact, a.State, a.Note, fingerprint, a.Expires, now).Scan(&a.ID, &a.Revision)
		} else {
			e = tx.QueryRow(ctx, `UPDATE listings SET title=$1,body=$2,price=$3,contact=$4,state=$5,moderator_note=$6,fingerprint=$7,expires_at=$8,updated_at=$9,revision=revision+1 WHERE id=$10 AND revision=$11 RETURNING revision`, a.Title, a.Body, a.Price, a.Contact, a.State, a.Note, fingerprint, a.Expires, now, a.ID, a.Revision).Scan(&a.Revision)
		}
	default:
		if a.User == nil {
			return a, catalog.ErrInvalid
		}
		if a.ID == 0 {
			e = tx.QueryRow(ctx, `INSERT INTO lost_items(user_id,kind,title,body,place,contact,state,note,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$9) RETURNING id,revision`, a.User, a.Kind, a.Title, a.Body, a.Place, a.Contact, a.State, a.Note, now).Scan(&a.ID, &a.Revision)
		} else {
			e = tx.QueryRow(ctx, `UPDATE lost_items SET kind=$1,title=$2,body=$3,place=$4,contact=$5,state=$6,note=$7,updated_at=$8,revision=revision+1 WHERE id=$9 AND revision=$10 RETURNING revision`, a.Kind, a.Title, a.Body, a.Place, a.Contact, a.State, a.Note, now, a.ID, a.Revision).Scan(&a.Revision)
		}
	}
	if e == pgx.ErrNoRows {
		e = catalog.ErrConflict
	}
	return a, e
}
func Remove(ctx context.Context, tx pgx.Tx, kind string, id, user int64, revision int, admin bool) error {
	table := Table(kind)
	if table == "" {
		return catalog.ErrInvalid
	}
	state := "state='deleted',contact=''"
	if kind == "channels" {
		state = "status='deleted'"
	}
	tag, e := tx.Exec(ctx, `UPDATE `+table+` SET `+state+`,revision=revision+1 WHERE id=$1 AND revision=$2 AND ($3 OR user_id=$4)`, id, revision, admin, user)
	if e == nil && tag.RowsAffected() != 1 {
		return catalog.ErrConflict
	}
	return e
}
func Report(ctx context.Context, tx pgx.Tx, user, id int64, reason string) error {
	a, e := Get(ctx, tx, "market", id, user, false)
	if e != nil || !Public(a, time.Now().Unix()) {
		return catalog.ErrMissing
	}
	if !catalog.Text(reason, 1000) {
		return catalog.ErrInvalid
	}
	if e = support.Limit(ctx, tx, fmt.Sprintf("report:%d", user), 10, 3600); e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO reports(user_id,listing_id,reason,created_at) VALUES($1,$2,$3,$4)`, user, id, reason, time.Now().Unix())
	return e
}

// RemoveBatch archives an entire selection or none, with one lock query and one update.
func RemoveBatch(ctx context.Context, tx pgx.Tx, kind string, items []catalog.Revision) error {
	table := Table(kind)
	if table == "" || len(items) < 1 || len(items) > 100 {
		return catalog.ErrInvalid
	}
	ids := []int64{}
	versions := map[int64]int{}
	for _, i := range items {
		id, e := strconv.ParseInt(i.ID, 10, 64)
		if e != nil || id < 1 || versions[id] != 0 || i.Revision < 1 {
			return catalog.ErrInvalid
		}
		ids = append(ids, id)
		versions[id] = i.Revision
	}
	rows, e := tx.Query(ctx, `SELECT id,revision FROM `+table+` WHERE id=ANY($1) ORDER BY id FOR UPDATE`, ids)
	if e != nil {
		return e
	}
	count := 0
	valid := true
	for rows.Next() {
		var id int64
		var revision int
		if e = rows.Scan(&id, &revision); e != nil {
			rows.Close()
			return e
		}
		count++
		valid = valid && versions[id] == revision
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if count != len(ids) || !valid {
		return catalog.ErrConflict
	}
	state := "state='deleted',contact=''"
	if kind == "channels" {
		state = "status='deleted'"
	}
	_, e = tx.Exec(ctx, `UPDATE `+table+` SET `+state+`,revision=revision+1 WHERE id=ANY($1)`, ids)
	return e
}
