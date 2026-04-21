package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pressly/goose/v3"

	"github.com/walker-qiang/personal-finance/internal/config"
	"github.com/walker-qiang/personal-finance/internal/db"
	"github.com/walker-qiang/personal-finance/internal/db/migrations"
	"github.com/walker-qiang/personal-finance/internal/db/store"
	"github.com/walker-qiang/personal-finance/internal/publish"
)

// newTestAPI spins up a fresh on-disk SQLite DB (file: scheme + tmp dir; the
// pure-Go modernc driver has rough edges with :memory: across goroutines, so
// we use a tmp file path which is also closer to prod). Migrations are run
// from the embedded FS so the test doesn't need `make tools` to have run.
func newTestAPI(t *testing.T) (*API, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("goose dialect: %v", err)
	}
	if err := goose.Up(conn, "."); err != nil {
		t.Fatalf("goose up: %v", err)
	}

	cfg := &config.Config{
		PublishWorktree: dir, // unused for bucket-targets tests; reused for lastPublish ones
	}
	st := store.New(conn)
	api := &API{Store: st, Job: publish.New(cfg, st)}

	r := gin.New()
	api.Register(r)
	return api, r
}

// doJSON performs a JSON request against the test router and returns the
// parsed body + status. Body is decoded into a generic map so we can spot-
// check fields without spelling out full DTO structs in every assertion.
func doJSON(t *testing.T, r *gin.Engine, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var buf io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		buf = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var out map[string]any
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("unmarshal response (status=%d body=%q): %v",
				w.Code, w.Body.String(), err)
		}
	}
	return w.Code, out
}

// ---------- bucket-targets ----------

func TestListBucketTargets_EmptyReturns3RowsAllUnset(t *testing.T) {
	_, r := newTestAPI(t)

	code, body := doJSON(t, r, "GET", "/api/finance/bucket-targets", nil)
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%v", code, body)
	}

	rows, ok := body["bucket_targets"].([]any)
	if !ok {
		t.Fatalf("missing bucket_targets array: %v", body)
	}
	if len(rows) != 3 {
		t.Errorf("want 3 rows (cash/stable/growth), got %d", len(rows))
	}
	gotBuckets := []string{}
	for _, raw := range rows {
		row := raw.(map[string]any)
		gotBuckets = append(gotBuckets, row["bucket"].(string))
		if row["is_set"].(bool) {
			t.Errorf("bucket=%v should be is_set=false on empty DB", row["bucket"])
		}
		if row["target_pct"] != nil {
			t.Errorf("bucket=%v target_pct should be nil, got %v", row["bucket"], row["target_pct"])
		}
	}
	wantOrder := []string{"cash", "stable", "growth"}
	for i, b := range wantOrder {
		if gotBuckets[i] != b {
			t.Errorf("row[%d] bucket = %q, want %q", i, gotBuckets[i], b)
		}
	}

	if body["sum_pct"] != nil {
		t.Errorf("sum_pct should be nil when nothing is set, got %v", body["sum_pct"])
	}
}

func TestUpsertBucketTarget_OK_Idempotent_Sums(t *testing.T) {
	_, r := newTestAPI(t)

	// First insert.
	code, body := doJSON(t, r, "PUT", "/api/finance/bucket-targets",
		map[string]any{"bucket": "cash", "target_pct": 20.0, "notes": "v1"})
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%v", code, body)
	}
	bt := body["bucket_target"].(map[string]any)
	if bt["bucket"] != "cash" || bt["target_pct"].(float64) != 20.0 || bt["is_set"] != true {
		t.Errorf("unexpected upsert body: %v", bt)
	}

	// Second insert on same bucket = overwrite (idempotent).
	code, body = doJSON(t, r, "PUT", "/api/finance/bucket-targets",
		map[string]any{"bucket": "cash", "target_pct": 25.5, "notes": "v2"})
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%v", code, body)
	}
	bt = body["bucket_target"].(map[string]any)
	if bt["target_pct"].(float64) != 25.5 || bt["notes"] != "v2" {
		t.Errorf("expected overwrite to 25.5/v2, got %v", bt)
	}

	// Add another bucket; verify list returns set+unset rows + correct sum.
	if code, _ := doJSON(t, r, "PUT", "/api/finance/bucket-targets",
		map[string]any{"bucket": "stable", "target_pct": 50.0}); code != http.StatusOK {
		t.Fatalf("stable upsert failed: %d", code)
	}

	code, body = doJSON(t, r, "GET", "/api/finance/bucket-targets", nil)
	if code != http.StatusOK {
		t.Fatalf("list status=%d body=%v", code, body)
	}
	if got := body["sum_pct"].(float64); got != 75.5 {
		t.Errorf("sum_pct want 75.5, got %v", got)
	}
	rows := body["bucket_targets"].([]any)
	setStates := map[string]bool{}
	for _, raw := range rows {
		row := raw.(map[string]any)
		setStates[row["bucket"].(string)] = row["is_set"].(bool)
	}
	want := map[string]bool{"cash": true, "stable": true, "growth": false}
	for k, v := range want {
		if setStates[k] != v {
			t.Errorf("is_set[%s] = %v, want %v", k, setStates[k], v)
		}
	}
}

func TestUpsertBucketTarget_Validation(t *testing.T) {
	_, r := newTestAPI(t)
	cases := []struct {
		name string
		body map[string]any
	}{
		{"unknown bucket", map[string]any{"bucket": "moon", "target_pct": 10.0}},
		{"empty bucket", map[string]any{"bucket": "", "target_pct": 10.0}},
		{"target negative", map[string]any{"bucket": "cash", "target_pct": -1.0}},
		{"target over 100", map[string]any{"bucket": "cash", "target_pct": 100.01}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doJSON(t, r, "PUT", "/api/finance/bucket-targets", tc.body)
			if code != http.StatusBadRequest {
				t.Errorf("want 400, got %d body=%v", code, body)
			}
			if _, ok := body["error"]; !ok {
				t.Errorf("expected error field in body, got %v", body)
			}
		})
	}
}

func TestDeleteBucketTarget_NotFoundThenSet(t *testing.T) {
	_, r := newTestAPI(t)

	// Delete non-existent row should 404 (sql.ErrNoRows path).
	code, _ := doJSON(t, r, "DELETE", "/api/finance/bucket-targets/cash", nil)
	if code != http.StatusNotFound {
		t.Errorf("first delete on empty bucket want 404, got %d", code)
	}

	// Set + delete = 200; subsequent list should still return 3 rows but cash unset.
	if code, _ := doJSON(t, r, "PUT", "/api/finance/bucket-targets",
		map[string]any{"bucket": "cash", "target_pct": 30.0}); code != http.StatusOK {
		t.Fatalf("upsert failed: %d", code)
	}
	code, body := doJSON(t, r, "DELETE", "/api/finance/bucket-targets/cash", nil)
	if code != http.StatusOK {
		t.Fatalf("delete after set want 200, got %d body=%v", code, body)
	}
	if body["deleted_bucket"] != "cash" {
		t.Errorf("deleted_bucket = %v, want cash", body["deleted_bucket"])
	}

	// Re-list: cash back to is_set=false, sum_pct nil.
	_, body = doJSON(t, r, "GET", "/api/finance/bucket-targets", nil)
	if body["sum_pct"] != nil {
		t.Errorf("after deleting only set bucket, sum_pct should be nil, got %v", body["sum_pct"])
	}
}

func TestDeleteBucketTarget_BadBucket(t *testing.T) {
	_, r := newTestAPI(t)
	code, body := doJSON(t, r, "DELETE", "/api/finance/bucket-targets/notabucket", nil)
	if code != http.StatusBadRequest {
		t.Errorf("want 400 on bad bucket name, got %d body=%v", code, body)
	}
}

// ---------- publish/last ----------

// makeFakeWorktree creates a throwaway git repo in a temp dir with one commit.
// `withAutoPublish=true` makes that commit's subject start with [auto-publish]
// so lastPublish() can find it. We pass that path as PublishWorktree.
func makeFakeWorktree(t *testing.T, withAutoPublish bool) string {
	t.Helper()
	dir := t.TempDir()
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		// Disable global hooks/templates that might prompt or touch ssh-agent.
		cmd.Env = append(cmd.Env,
			"HOME="+dir,
			"GIT_TERMINAL_PROMPT=0",
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@local",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@local",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q", "-b", "publish-main")
	runGit("config", "user.email", "t@local")
	runGit("config", "user.name", "t")
	runGit("commit", "--allow-empty", "-m", "initial")

	if withAutoPublish {
		// Sleep a hair so commit timestamps are monotonically increasing if the
		// test reuses this for multiple commits later.
		time.Sleep(10 * time.Millisecond)
		runGit("commit", "--allow-empty", "-m", "[auto-publish] finance/exports: 2026-04-20")
	}
	return dir
}

func TestLastPublish_NoCommitYet(t *testing.T) {
	api, r := newTestAPI(t)
	// Override worktree to a fresh repo with NO [auto-publish] commit.
	api.Job = publish.New(&config.Config{PublishWorktree: makeFakeWorktree(t, false)}, api.Store)

	code, body := doJSON(t, r, "GET", "/api/finance/publish/last", nil)
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%v", code, body)
	}
	if body["last"] != nil {
		t.Errorf("expected last=nil on fresh worktree, got %v", body["last"])
	}
}

func TestLastPublish_FindsAutoPublishCommit(t *testing.T) {
	api, r := newTestAPI(t)
	api.Job = publish.New(&config.Config{PublishWorktree: makeFakeWorktree(t, true)}, api.Store)

	code, body := doJSON(t, r, "GET", "/api/finance/publish/last", nil)
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%v", code, body)
	}
	last, ok := body["last"].(map[string]any)
	if !ok {
		t.Fatalf("expected last to be an object, got %v", body["last"])
	}
	if sha, _ := last["commit_sha"].(string); len(sha) != 40 {
		t.Errorf("commit_sha looks wrong (want 40-char hex): %q", sha)
	}
	if subj, _ := last["subject"].(string); !strings.HasPrefix(subj, "[auto-publish]") {
		t.Errorf("subject should start with [auto-publish], got %q", subj)
	}
	if br, _ := last["branch"].(string); br != "publish-main" {
		t.Errorf("branch want publish-main, got %q", br)
	}
}

func TestLastPublish_BadWorktree(t *testing.T) {
	api, r := newTestAPI(t)
	api.Job = publish.New(&config.Config{PublishWorktree: "/nonexistent/path/should-fail"}, api.Store)
	code, body := doJSON(t, r, "GET", "/api/finance/publish/last", nil)
	if code != http.StatusInternalServerError {
		t.Errorf("expected 500 on bad worktree, got %d body=%v", code, body)
	}
}
