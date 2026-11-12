package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/Haj-amirreza/tg_go/backend/internal/cache"
	"github.com/jackc/pgx/v5"
	"time"
)

// Generation is changed in the SAME transaction as every catalog mutation.
// Public books contain no user identity; authorization and send-time guards are never cached.
func CachedBooks(ctx context.Context, tx pgx.Tx, store *cache.Cache, query, category string, page int) (Page[Book], error) {
	if store == nil {
		return Books(ctx, tx, query, category, page)
	}
	var generation int64
	if e := tx.QueryRow(ctx, "SELECT revision FROM catalog_generations WHERE scope='books'").Scan(&generation); e != nil {
		return Page[Book]{}, e
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s:%s", generation, page, category, Normalize(query))))
	key := "books:" + hex.EncodeToString(sum[:])
	raw, e := store.Get(ctx, key, 30*time.Second, func(ctx context.Context) ([]byte, error) {
		result, e := Books(ctx, tx, query, category, page)
		if e != nil {
			return nil, e
		}
		return json.Marshal(result)
	})
	if e != nil {
		return Page[Book]{}, e
	}
	var out Page[Book]
	e = json.Unmarshal(raw, &out)
	return out, e
}
