package catalog

import (
	"context"
	"github.com/jackc/pgx/v5"
	"strconv"
)

type Topic struct {
	ID     int64  `json:"id,string"`
	Parent *int64 `json:"parentId"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
}

func Topics(ctx context.Context, tx pgx.Tx, parent int64, units bool) ([]Topic, error) {
	rows, e := tx.Query(ctx, `SELECT id,parent_id,kind,name FROM taxonomy WHERE (($1::bigint=0 AND parent_id IS NULL AND (kind IN ('faculty','graduate') OR ($2 AND kind='unit'))) OR parent_id=NULLIF($1,0)) AND kind IN ('faculty','graduate','unit','department','major','degree') ORDER BY sort_order,id`, parent, units)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Topic{}
	for rows.Next() {
		var t Topic
		if e = rows.Scan(&t.ID, &t.Parent, &t.Kind, &t.Name); e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func TopicMembership(ctx context.Context, tx pgx.Tx, content int64, metadata map[string]any) error {
	value, exists := metadata["topic_ids"]
	if !exists {
		return nil
	}
	ids := []int64{}
	add := func(v any) error {
		var id int64
		switch n := v.(type) {
		case string:
			var e error
			id, e = strconv.ParseInt(n, 10, 64)
			if e != nil {
				return ErrInvalid
			}
		case float64:
			id = int64(n)
			if n != float64(id) {
				return ErrInvalid
			}
		case int64:
			id = n
		case int:
			id = int64(n)
		default:
			return ErrInvalid
		}
		if id < 1 {
			return ErrInvalid
		}
		for _, old := range ids {
			if old == id {
				return nil
			}
		}
		ids = append(ids, id)
		return nil
	}
	switch list := value.(type) {
	case []any:
		for _, v := range list {
			if e := add(v); e != nil {
				return e
			}
		}
	case []string:
		for _, v := range list {
			if e := add(v); e != nil {
				return e
			}
		}
	case []int64:
		for _, v := range list {
			if e := add(v); e != nil {
				return e
			}
		}
	case nil:
	default:
		return ErrInvalid
	}
	if len(ids) > 100 {
		return ErrInvalid
	}
	var count int
	if e := tx.QueryRow(ctx, `SELECT count(*) FROM taxonomy WHERE id=ANY($1)`, ids).Scan(&count); e != nil {
		return e
	}
	if count != len(ids) {
		return ErrInvalid
	}
	if _, e := tx.Exec(ctx, `DELETE FROM content_topics WHERE content_id=$1`, content); e != nil {
		return e
	}
	if len(ids) == 0 {
		return nil
	}
	_, e := tx.Exec(ctx, `INSERT INTO content_topics(content_id,topic_id) SELECT $1,unnest($2::bigint[])`, content, ids)
	return e
}
