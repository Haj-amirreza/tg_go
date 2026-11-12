package leisure

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"github.com/Haj-amirreza/tg_go/backend/internal/cache"
	"github.com/Haj-amirreza/tg_go/backend/internal/catalog"
	"github.com/Haj-amirreza/tg_go/backend/internal/rich"
	"github.com/jackc/pgx/v5"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed defaults.json
var seed []byte
var Kinds = map[string][]string{"poem": {"poet", "name", "title", "source", "verses", "interpretation"}, "recommendation": {"category", "title", "author", "kind", "summary", "why"}, "word": {"level", "text"}, "quote": {"text"}, "excerpt": {"text", "source"}, "riddle": {"question", "answer"}}

type Records map[string]map[string]map[string]any

func base() Records {
	var source map[string][]map[string]any
	json.Unmarshal(seed, &source)
	out := Records{}
	for kind, rows := range source {
		out[kind] = map[string]map[string]any{}
		for i, r := range rows {
			out[kind]["base_"+strconv.Itoa(i)] = r
		}
	}
	return out
}
func Load(ctx context.Context, tx pgx.Tx, store *cache.Cache) (Records, int64, error) {
	var gen int64
	e := tx.QueryRow(ctx, `SELECT revision FROM catalog_generations WHERE scope='leisure'`).Scan(&gen)
	if e != nil {
		return nil, 0, e
	}
	fetch := func(context.Context) ([]byte, error) {
		out := base()
		var value string
		e := tx.QueryRow(ctx, `SELECT value FROM settings WHERE name='leisure_overlay' AND NOT is_secret`).Scan(&value)
		if e != nil && e != pgx.ErrNoRows {
			return nil, e
		}
		if e == nil {
			var overlay map[string]map[string]map[string]any
			if json.Unmarshal([]byte(value), &overlay) != nil {
				return nil, catalog.ErrInvalid
			}
			for kind, rows := range overlay {
				if _, ok := Kinds[kind]; !ok {
					continue
				}
				for id, row := range rows {
					if row == nil {
						delete(out[kind], id)
					} else {
						out[kind][id] = row
					}
				}
			}
		}
		return json.Marshal(out)
	}
	var raw []byte
	if store == nil {
		raw, e = fetch(ctx)
	} else {
		raw, e = store.Get(ctx, fmt.Sprintf("leisure:%d", gen), 5*time.Minute, fetch)
	}
	var out Records
	if e == nil {
		e = json.Unmarshal(raw, &out)
	}
	return out, gen, e
}
func Validate(kind string, row map[string]any) error {
	fields, ok := Kinds[kind]
	if !ok {
		return catalog.ErrInvalid
	}
	raw, e := json.Marshal(row)
	if e != nil || len(raw) > 24000 {
		return catalog.ErrInvalid
	}
	for _, f := range fields {
		if f == "interpretation" && catalog.MetaText(row, "poet") != "hafez" {
			continue
		}
		if f == "verses" {
			a, ok := row[f].([]any)
			if !ok || len(a) == 0 || len(a) > 300 {
				return catalog.ErrInvalid
			}
			for _, v := range a {
				s, ok := v.(string)
				if !ok || !catalog.Text(s, 1000) {
					return catalog.ErrInvalid
				}
			}
		} else {
			v, ok := row[f].(string)
			if !ok || !catalog.Text(v, 12000) {
				return catalog.ErrInvalid
			}
		}
	}
	if kind == "poem" {
		p := catalog.MetaText(row, "poet")
		if p != "hafez" && p != "saadi" && p != "rumi" || !rich.URL(catalog.MetaText(row, "source")) {
			return catalog.ErrInvalid
		}
	}
	if kind == "word" {
		v := catalog.MetaText(row, "level")
		if v != "easy" && v != "medium" && v != "hard" {
			return catalog.ErrInvalid
		}
	}
	if kind == "recommendation" {
		v := catalog.MetaText(row, "category")
		if v != "short" && v != "iran" && v != "thought" && v != "classic" {
			return catalog.ErrInvalid
		}
	}
	return nil
}

type Change struct {
	Kind     string         `json:"kind"`
	ID       string         `json:"id"`
	Row      map[string]any `json:"row"`
	Revision int64          `json:"revision"`
}

func Save(ctx context.Context, tx pgx.Tx, c Change) (int64, error) {
	return Apply(ctx, tx, c.Kind, map[string]map[string]any{c.ID: c.Row}, c.Revision)
}

// Apply invalidates the public cache once for an atomic selection of at most 100 records.
func Apply(ctx context.Context, tx pgx.Tx, kind string, changes map[string]map[string]any, revision int64) (int64, error) {
	fields, ok := Kinds[kind]
	if !ok || len(changes) == 0 || len(changes) > 100 {
		return 0, catalog.ErrInvalid
	}
	for id, row := range changes {
		if !regexp.MustCompile(`^(base_[0-9]{1,5}|custom_[a-f0-9]{12,32})$`).MatchString(id) {
			return 0, catalog.ErrInvalid
		}
		if row != nil {
			if e := Validate(kind, row); e != nil {
				return 0, e
			}
			clean := map[string]any{}
			for _, f := range fields {
				clean[f] = row[f]
			}
			changes[id] = clean
		}
	}
	var gen int64
	e := tx.QueryRow(ctx, `SELECT revision FROM catalog_generations WHERE scope='leisure' FOR UPDATE`).Scan(&gen)
	if e != nil {
		return 0, e
	}
	if gen != revision {
		return 0, catalog.ErrConflict
	}
	overlay := Records{}
	var raw string
	e = tx.QueryRow(ctx, `SELECT value FROM settings WHERE name='leisure_overlay' AND NOT is_secret`).Scan(&raw)
	if e != nil && e != pgx.ErrNoRows {
		return 0, e
	}
	if e == nil && json.Unmarshal([]byte(raw), &overlay) != nil {
		return 0, catalog.ErrInvalid
	}
	if overlay[kind] == nil {
		overlay[kind] = map[string]map[string]any{}
	}
	for id, row := range changes {
		overlay[kind][id] = row
	}
	data, _ := json.Marshal(overlay)
	if len(data) > 2<<20 {
		return 0, catalog.ErrInvalid
	}
	_, e = tx.Exec(ctx, `INSERT INTO settings(name,value,updated_at) VALUES('leisure_overlay',$1,$2) ON CONFLICT(name) DO UPDATE SET value=EXCLUDED.value,updated_at=EXCLUDED.updated_at`, string(data), time.Now().Unix())
	if e == nil {
		_, e = tx.Exec(ctx, `UPDATE catalog_generations SET revision=revision+1 WHERE scope='leisure'`)
	}
	return gen + 1, e
}
func Ordered(records Records, kind string) []string {
	ids := []string{}
	for id := range records[kind] {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := ids[i], ids[j]
		if strings.HasPrefix(a, "base_") && strings.HasPrefix(b, "base_") {
			x, _ := strconv.Atoi(strings.TrimPrefix(a, "base_"))
			y, _ := strconv.Atoi(strings.TrimPrefix(b, "base_"))
			return x < y
		}
		if strings.HasPrefix(a, "base_") != strings.HasPrefix(b, "base_") {
			return strings.HasPrefix(a, "base_")
		}
		return a < b
	})
	return ids
}
