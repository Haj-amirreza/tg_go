package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrInvalid = errors.New("invalid catalog input")
var ErrConflict = errors.New("catalog revision changed")
var ErrMissing = errors.New("catalog item unavailable")

func Normalize(value string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		switch r {
		case 'ي', 'ى':
			return 'ی'
		case 'ك':
			return 'ک'
		case '\u200c':
			return ' '
		}
		if r >= '۰' && r <= '۹' {
			return '0' + r - '۰'
		}
		if r >= '٠' && r <= '٩' {
			return '0' + r - '٠'
		}
		return unicode.ToLower(r)
	}, value)), " ")
}
func Text(value string, max int) bool {
	if strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > max {
		return false
	}
	for _, r := range value {
		if r < 32 && r != '\n' && r != '\t' {
			return false
		}
	}
	return true
}
func ProfessorField(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	max := 250
	if field == "courses" {
		max = 1000
	}
	if field == "name" {
		max = 200
	}
	if !Text(value, max) {
		return "", ErrInvalid
	}
	switch field {
	case "email":
		a, e := mail.ParseAddress(value)
		if e != nil || a.Address != value || !regexp.MustCompile(`@[^@\s]+\.[^@\s]+$`).MatchString(value) {
			return "", ErrInvalid
		}
	case "phone":
		value = Normalize(value)
		if !regexp.MustCompile(`^\+?[0-9][0-9 ()-]{3,28}$`).MatchString(value) {
			return "", ErrInvalid
		}
	case "handle":
		value = regexp.MustCompile(`(?i)^https?://(?:t\.me|telegram\.me)/`).ReplaceAllString(value, "")
		if !regexp.MustCompile(`^@?[A-Za-z][A-Za-z0-9_]{3,31}$`).MatchString(value) {
			return "", ErrInvalid
		}
		value = "@" + strings.TrimPrefix(value, "@")
	}
	return value, nil
}

type Content struct {
	ID          int64          `json:"id,string"`
	Kind        string         `json:"kind"`
	Title       string         `json:"title"`
	Body        string         `json:"body"`
	Metadata    map[string]any `json:"metadata"`
	Status      string         `json:"status"`
	Version     int            `json:"version"`
	PublishedAt *int64         `json:"publishedAt"`
	ExpiresAt   *int64         `json:"expiresAt"`
}
type Book struct {
	ID       int64          `json:"id,string"`
	Title    string         `json:"title"`
	Details  map[string]any `json:"details"`
	Category string         `json:"category"`
	Revision int            `json:"revision"`
}
type Page[T any] struct {
	Rows  []T `json:"rows"`
	Page  int `json:"page"`
	Pages int `json:"pages"`
	Total int `json:"total"`
}

var Categories = []struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Pattern string `json:"-"`
}{
	{"biology", "زیست‌شناسی و ژنتیک", "زیست|ژنتیک|گیاه|جانور"}, {"science", "علوم پایه؛ ریاضی، فیزیک و شیمی", "ریاضی|فیزیک|شیمی"}, {"engineering", "مهندسی و رایانه", "مهندسی|کامپیوتر|رایانه|عمران|الکترونیک"}, {"medicine", "پزشکی و سلامت", "پزشکی|پرستاری|بهداشت"}, {"agriculture", "کشاورزی و محیط زیست", "کشاورزی|محیط زیست|آبزی"}, {"literature", "ادبیات و زبان", "ادبیات|شعر|داستان|زبان|فرهنگ لغت"}, {"philosophy", "فلسفه، روان‌شناسی و دین", "فلسفه|روانشناسی|روان شناسی|ادیان|اسلام"}, {"history", "تاریخ و جغرافیا", "تاریخ|جغرافیا|باستان"}, {"social", "علوم اجتماعی، مدیریت و آموزش", "اقتصاد|مدیریت|حقوق|جامعه|آموزش"}, {"general", "عمومی و سایر", ""}}

var categoryMatchers = func() []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(Categories))
	for i, c := range Categories {
		if c.Pattern != "" {
			out[i] = regexp.MustCompile(c.Pattern)
		}
	}
	return out
}()

func Classify(text string) string {
	for i, c := range Categories {
		if categoryMatchers[i] != nil && categoryMatchers[i].MatchString(text) {
			return c.ID
		}
	}
	return "general"
}
func CategoryOK(id string) bool {
	for _, c := range Categories {
		if c.ID == id {
			return true
		}
	}
	return false
}
func MetaText(meta map[string]any, key string) string { s, _ := meta[key].(string); return s }

const columns = "id,kind,title,body,metadata,status,version,published_at,expires_at"

func scan(row pgx.Row) (Content, error) {
	var c Content
	var meta string
	err := row.Scan(&c.ID, &c.Kind, &c.Title, &c.Body, &meta, &c.Status, &c.Version, &c.PublishedAt, &c.ExpiresAt)
	if err != nil {
		return c, err
	}
	err = json.Unmarshal([]byte(meta), &c.Metadata)
	return c, err
}
func Visible(c Content, tags []string, approved bool, now int64) bool {
	if c.Status != "published" || (c.PublishedAt != nil && *c.PublishedAt > now) || (c.ExpiresAt != nil && *c.ExpiresAt <= now) {
		return false
	}
	tag := MetaText(c.Metadata, "audience_tag")
	if tag != "" {
		found := false
		for _, t := range tags {
			found = found || tag == t
		}
		if !found {
			return false
		}
	}
	return c.Kind != "links" || MetaText(c.Metadata, "link_type") == "channel" || approved
}
func Get(ctx context.Context, tx pgx.Tx, id int64) (Content, error) {
	c, e := scan(tx.QueryRow(ctx, "SELECT "+columns+" FROM contents WHERE id=$1", id))
	if errors.Is(e, pgx.ErrNoRows) {
		e = ErrMissing
	}
	return c, e
}
func ReadPublished(ctx context.Context, tx pgx.Tx, id int64, tags []string, approved bool) (Content, error) {
	c, e := Get(ctx, tx, id)
	if e == nil && !Visible(c, tags, approved, time.Now().Unix()) {
		e = ErrMissing
	}
	return c, e
}
func Search(ctx context.Context, tx pgx.Tx, kind, query string, page int, tags []string, approved bool) (Page[Content], error) {
	return SearchFiltered(ctx, tx, kind, query, page, tags, approved, nil)
}
func SearchFiltered(ctx context.Context, tx pgx.Tx, kind, query string, page int, tags []string, approved bool, filters map[string]string) (Page[Content], error) {
	out := Page[Content]{Rows: []Content{}}
	if page < 0 || page > 10000 || utf8.RuneCountInString(query) > 160 {
		return out, ErrInvalid
	}
	args := []any{kind, time.Now().Unix(), tags, approved}
	where := `status='published' AND (published_at IS NULL OR published_at<=$2) AND (expires_at IS NULL OR expires_at>$2) AND (COALESCE(metadata::jsonb->>'audience_tag','')='' OR metadata::jsonb->>'audience_tag'=ANY($3)) AND (kind<>'links' OR metadata::jsonb->>'link_type'='channel' OR $4)`
	if kind == "people" {
		where += ` AND kind IN ('professors','contacts','handles') AND $1='people'`
	} else if kind == "systems" {
		where += ` AND kind IN ('guides','faq') AND $1='systems'`
	} else if kind == "professors" {
		where += ` AND (kind='professors' OR (kind='contacts' AND metadata::jsonb->>'person_role' IN ('professor','adjunct'))) AND $1='professors'`
	} else {
		where += " AND kind=$1"
	}
	for key, value := range filters {
		if key == "topic" {
			id, e := strconv.ParseInt(value, 10, 64)
			if e != nil || id < 0 {
				return out, ErrInvalid
			}
			args = append(args, id)
			placeholder := fmt.Sprintf("$%d", len(args))
			if id == 0 {
				where += ` AND topic_id IS NULL AND NOT EXISTS(SELECT 1 FROM content_topics ct WHERE ct.content_id=contents.id)`
			} else {
				where += ` AND (topic_id=` + placeholder + ` OR EXISTS(SELECT 1 FROM content_topics ct WHERE ct.content_id=contents.id AND ct.topic_id=` + placeholder + `))`
			}
			if id == 0 {
				where += " AND " + placeholder + "::bigint=0"
			}
			continue
		}

		switch key {
		case "system", "link_type", "link_category", "person_role", "course_level":
		default:
			return out, ErrInvalid
		}
		args = append(args, value)
		fallback := ""
		if key == "person_role" {
			fallback = "professor"
		}
		if key == "system" {
			fallback = "other"
		}
		if key == "link_type" {
			fallback = "unclassified"
		}
		where += fmt.Sprintf(" AND COALESCE(metadata::jsonb->>'%s','%s')=$%d", key, fallback, len(args))
	}
	for _, term := range strings.Fields(Normalize(query))[:min(len(strings.Fields(Normalize(query))), 12)] {
		args = append(args, "%"+strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(term)+"%")
		where += fmt.Sprintf(" AND search_text LIKE $%d ESCAPE '!'", len(args))
	}
	if e := tx.QueryRow(ctx, "SELECT count(*) FROM contents WHERE "+where, args...).Scan(&out.Total); e != nil {
		return out, e
	}
	out.Pages = max(1, (out.Total+9)/10)
	out.Page = min(page, out.Pages-1)
	order := "pinned DESC,priority DESC,id DESC"
	if kind == "professors" {
		order = "title,id"
	}
	args = append(args, out.Page*10)
	rows, e := tx.Query(ctx, "SELECT "+columns+" FROM contents WHERE "+where+" ORDER BY "+order+fmt.Sprintf(" LIMIT 10 OFFSET $%d", len(args)), args...)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		c, e := scan(rows)
		if e != nil {
			return out, e
		}
		out.Rows = append(out.Rows, c)
	}
	return out, rows.Err()
}
func Save(ctx context.Context, tx pgx.Tx, c Content, actor int64, canPublish bool) (Content, error) {
	if !Text(c.Title, 200) || !Text(c.Kind, 30) || utf8.RuneCountInString(c.Body) > 30000 {
		return c, ErrInvalid
	}
	switch c.Status {
	case "draft", "pending", "published", "archived":
	default:
		return c, ErrInvalid
	}
	if c.Status == "published" && !canPublish {
		return c, ErrInvalid
	}
	if c.Kind == "professors" || c.Kind == "contacts" {
		for metaKey, field := range map[string]string{"office_phone": "phone", "personal_phone": "phone", "foreign_id": "handle", "email": "email"} {
			if raw, exists := c.Metadata[metaKey]; exists && raw != nil {
				if _, ok := raw.(string); !ok {
					return c, ErrInvalid
				}
			}
			if value := MetaText(c.Metadata, metaKey); value != "" {
				value, e := ProfessorField(field, value)
				if e != nil {
					return c, e
				}
				c.Metadata[metaKey] = value
			}
		}
	}
	if c.Metadata == nil {
		c.Metadata = map[string]any{}
	}
	meta, e := json.Marshal(c.Metadata)
	if e != nil || len(meta) > 30000 {
		return c, ErrInvalid
	}
	search := Normalize(c.Title + " " + c.Body + " " + string(meta))
	now := time.Now().Unix()
	if c.ID == 0 {
		c.Version = 1
		e = tx.QueryRow(ctx, `INSERT INTO contents(kind,title,body,metadata,status,published_at,expires_at,created_at,updated_at,search_text,reviewed_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8,$9,$10) RETURNING id`, c.Kind, c.Title, c.Body, string(meta), c.Status, c.PublishedAt, c.ExpiresAt, now, search, actor).Scan(&c.ID)
	} else {
		old, e := Get(ctx, tx, c.ID)
		if e != nil {
			return c, e
		}
		if old.Status == "published" && !canPublish {
			return c, ErrInvalid
		}
		tag, e := tx.Exec(ctx, `UPDATE contents SET kind=$1,title=$2,body=$3,metadata=$4,status=$5,published_at=$6,expires_at=$7,updated_at=$8,search_text=$9,reviewed_by=$10,version=version+1 WHERE id=$11 AND version=$12`, c.Kind, c.Title, c.Body, string(meta), c.Status, c.PublishedAt, c.ExpiresAt, now, search, actor, c.ID, c.Version)
		if e != nil {
			return c, e
		}
		if tag.RowsAffected() != 1 {
			return c, ErrConflict
		}
		c.Version++
	}
	if e != nil {
		return c, e
	}
	if e = TopicMembership(ctx, tx, c.ID, c.Metadata); e != nil {
		return c, e
	}
	raw, _ := json.Marshal(c)
	_, e = tx.Exec(ctx, `INSERT INTO content_revisions(content_id,version,snapshot,admin_id,created_at) VALUES($1,$2,$3,$4,$5)`, c.ID, c.Version, string(raw), actor, now)
	if e == nil {
		_, e = tx.Exec(ctx, "UPDATE catalog_generations SET revision=revision+1 WHERE scope='contents'")
	}
	return c, e
}
func Books(ctx context.Context, tx pgx.Tx, query, category string, page int) (Page[Book], error) {
	out := Page[Book]{Rows: []Book{}}
	if page < 0 || page > 10000 || utf8.RuneCountInString(query) > 160 || category != "" && !CategoryOK(category) {
		return out, ErrInvalid
	}
	where := "NOT deleted"
	args := []any{}
	if category != "" {
		args = append(args, category)
		where += " AND category=$1"
	}
	terms := strings.Fields(Normalize(query))
	for _, term := range terms[:min(len(terms), 12)] {
		args = append(args, "%"+strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(term)+"%")
		where += fmt.Sprintf(" AND search_text LIKE $%d ESCAPE '!'", len(args))
	}
	if e := tx.QueryRow(ctx, "SELECT count(*) FROM university_books WHERE "+where, args...).Scan(&out.Total); e != nil {
		return out, e
	}
	out.Pages = max(1, (out.Total+9)/10)
	out.Page = min(page, out.Pages-1)
	args = append(args, out.Page*10)
	rows, e := tx.Query(ctx, "SELECT id,title,details,category,revision FROM university_books WHERE "+where+fmt.Sprintf(" ORDER BY id LIMIT 10 OFFSET $%d", len(args)), args...)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var b Book
		var details string
		if e = rows.Scan(&b.ID, &b.Title, &details, &b.Category, &b.Revision); e != nil {
			return out, e
		}
		if json.Unmarshal([]byte(details), &b.Details) != nil {
			return out, ErrInvalid
		}
		out.Rows = append(out.Rows, b)
	}
	return out, rows.Err()
}
func GetBook(ctx context.Context, tx pgx.Tx, id int64) (Book, error) {
	var b Book
	var details string
	e := tx.QueryRow(ctx, "SELECT id,title,details,category,revision FROM university_books WHERE id=$1 AND NOT deleted", id).Scan(&b.ID, &b.Title, &details, &b.Category, &b.Revision)
	if e == nil {
		e = json.Unmarshal([]byte(details), &b.Details)
	}
	if errors.Is(e, pgx.ErrNoRows) {
		e = ErrMissing
	}
	return b, e
}

// ValidateSnapshot batches all send-time checks, avoiding one query per displayed item.
func ValidateSnapshot(ctx context.Context, tx pgx.Tx, versions map[string]int, bookIDs []int64, tags []string, approved bool) error {
	if len(versions) > 100 || len(bookIDs) > 100 {
		return ErrInvalid
	}
	if len(versions) > 0 {
		ids := []int64{}
		for key := range versions {
			id, e := strconv.ParseInt(key, 10, 64)
			if e != nil {
				return ErrInvalid
			}
			ids = append(ids, id)
		}
		rows, e := tx.Query(ctx, "SELECT "+columns+" FROM contents WHERE id=ANY($1)", ids)
		if e != nil {
			return e
		}
		seen := 0
		for rows.Next() {
			c, e := scan(rows)
			if e != nil {
				rows.Close()
				return e
			}
			if versions[strconv.FormatInt(c.ID, 10)] != c.Version || !Visible(c, tags, approved, time.Now().Unix()) {
				rows.Close()
				return ErrMissing
			}
			seen++
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if seen != len(versions) {
			return ErrMissing
		}
	}
	if len(bookIDs) > 0 {
		var count int
		e := tx.QueryRow(ctx, "SELECT count(*) FROM university_books WHERE id=ANY($1) AND NOT deleted", bookIDs).Scan(&count)
		if e != nil {
			return e
		}
		unique := map[int64]bool{}
		for _, id := range bookIDs {
			unique[id] = true
		}
		if count != len(unique) {
			return ErrMissing
		}
	}
	return nil
}
