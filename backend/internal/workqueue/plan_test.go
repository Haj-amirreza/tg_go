package workqueue

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestClaimExecutionPlan(t *testing.T) {
	if os.Getenv("NEXT_EXPLAIN") != "1" {
		t.Skip("explicit plan capture")
	}
	q := setup(t)
	ctx := context.Background()
	_, err := q.Pool.Exec(ctx, `INSERT INTO work_jobs(dedupe_key,kind,payload,stream_key) SELECT 'plan-'||n,'test','{}'::jsonb,'user:'||(n/10) FROM generate_series(1,10000)n; ANALYZE work_jobs`)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := q.Pool.Query(ctx, `EXPLAIN (ANALYZE,BUFFERS,FORMAT TEXT) SELECT j.id FROM work_jobs j WHERE state='pending' AND kind='test' AND available_at<=now() AND (stream_key='' OR NOT EXISTS(SELECT 1 FROM work_jobs older WHERE older.stream_key=j.stream_key AND older.id<j.id AND older.state IN ('pending','processing','uncertain'))) ORDER BY priority DESC,available_at,id FOR UPDATE OF j SKIP LOCKED LIMIT 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	lines := []string{}
	for rows.Next() {
		var line string
		rows.Scan(&line)
		lines = append(lines, line)
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	plan := strings.Join(lines, "\n")
	if !strings.Contains(plan, "work_claim") {
		t.Fatal("claim index not selected", plan)
	}
	if p := os.Getenv("NEXT_PLAN_REPORT"); p != "" {
		if os.WriteFile(p, []byte(plan+"\n"), 0600) != nil {
			t.Fatal("cannot write plan")
		}
	}
}
