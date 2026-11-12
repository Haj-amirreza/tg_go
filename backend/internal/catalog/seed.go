package catalog

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"io"
)

// SeedBooks is an explicit offline operation. It never runs on a student's first search.
// Existing source keys, IDs, edits and deletion flags are never overwritten.
func SeedBooks(ctx context.Context, tx pgx.Tx, input io.Reader) (int64, error) {
	gz, e := gzip.NewReader(input)
	if e != nil {
		return 0, e
	}
	defer gz.Close()
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(764629190)"); e != nil {
		return 0, e
	}
	if _, e = tx.Exec(ctx, `CREATE TEMP TABLE book_seed(source_key text,title text,details text,search_text text,category text) ON COMMIT DROP`); e != nil {
		return 0, e
	}
	scanner := bufio.NewScanner(gz)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	batch := [][]any{}
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		_, err := tx.CopyFrom(ctx, pgx.Identifier{"book_seed"}, []string{"source_key", "title", "details", "search_text", "category"}, pgx.CopyFromRows(batch))
		batch = nil
		return err
	}
	for scanner.Scan() {
		var in struct {
			Key     string         `json:"key"`
			Title   string         `json:"title"`
			Details map[string]any `json:"details"`
		}
		if json.Unmarshal(scanner.Bytes(), &in) != nil || len(in.Key) != 64 || !Text(in.Title, 2000) || in.Details == nil {
			return 0, ErrInvalid
		}
		raw, e := json.Marshal(in.Details)
		if e != nil {
			return 0, e
		}
		batch = append(batch, []any{in.Key, in.Title, string(raw), Normalize(in.Title + " " + string(raw)), Classify(in.Title + " " + string(raw))})
		if len(batch) == 500 {
			if e = flush(); e != nil {
				return 0, e
			}
		}
	}
	if scanner.Err() != nil {
		return 0, scanner.Err()
	}
	if e = flush(); e != nil {
		return 0, e
	}
	tag, e := tx.Exec(ctx, `INSERT INTO university_books(source_key,title,details,search_text,category) SELECT source_key,title,details,search_text,category FROM book_seed ON CONFLICT(source_key) DO NOTHING`)
	if e != nil {
		return 0, e
	}
	if tag.RowsAffected() > 0 {
		_, e = tx.Exec(ctx, "UPDATE catalog_generations SET revision=revision+1 WHERE scope='books'")
	}
	return tag.RowsAffected(), e
}
