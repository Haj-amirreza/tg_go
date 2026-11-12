package catalog

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"strconv"
	"time"
)

type Revision struct {
	ID       string `json:"id"`
	Version  int    `json:"version"`
	Revision int    `json:"revision"`
}

func Archive(ctx context.Context, tx pgx.Tx, items []Revision, books bool, actor int64, publish bool) error {
	if len(items) < 1 || len(items) > 100 {
		return ErrInvalid
	}
	ids := []int64{}
	versions := []int{}
	seen := map[int64]bool{}
	for _, item := range items {
		id, e := strconv.ParseInt(item.ID, 10, 64)
		if e != nil || id < 1 || seen[id] {
			return ErrInvalid
		}
		seen[id] = true
		ids = append(ids, id)
		version := item.Version
		if books {
			version = item.Revision
		}
		versions = append(versions, version)
	}
	table := "contents"
	if books {
		table = "university_books"
	}
	rows, e := tx.Query(ctx, "SELECT id FROM "+table+" WHERE id=ANY($1) ORDER BY id FOR UPDATE", ids)
	if e != nil {
		return e
	}
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	scope := "contents"
	if books {
		tag, e := tx.Exec(ctx, `UPDATE university_books c SET deleted=true,revision=c.revision+1 FROM unnest($1::bigint[],$2::integer[]) expected(id,version) WHERE c.id=expected.id AND c.revision=expected.version AND NOT deleted`, ids, versions)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != int64(len(items)) {
			return ErrConflict
		}
		scope = "books"
	} else {
		now := time.Now().Unix()
		tag, e := tx.Exec(ctx, `UPDATE contents c SET status='archived',version=c.version+1,updated_at=$4,reviewed_by=$5 FROM unnest($1::bigint[],$2::integer[]) expected(id,version) WHERE c.id=expected.id AND c.version=expected.version AND ($3 OR c.status<>'published')`, ids, versions, publish, now, actor)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != int64(len(items)) {
			return ErrConflict
		}
		_, e = tx.Exec(ctx, `INSERT INTO content_revisions(content_id,version,snapshot,admin_id,created_at) SELECT id,version,jsonb_build_object('id',id,'kind',kind,'title',title,'body',body,'metadata',metadata,'status',status,'version',version)::text,$2,$3 FROM contents WHERE id=ANY($1)`, ids, actor, now)
		if e != nil {
			return e
		}
	}
	_, e = tx.Exec(ctx, "UPDATE catalog_generations SET revision=revision+1 WHERE scope=$1", scope)
	return e
}
func ParseArchive(raw []byte) ([]Revision, error) {
	var in struct {
		Items []Revision `json:"items"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return nil, ErrInvalid
	}
	return in.Items, nil
}
