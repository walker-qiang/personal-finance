// Package publish implements the M1 publish job:
//
//   1. cd to ~/obsidian-wiki-publish-worktree, fetch + reset to origin/main
//   2. assert worktree is clean (else abort + log)
//   3. dump 4 tables -> finance/exports/{assets,snapshots,transactions,holdings}-YYYY-MM-DD.csv
//   4. git add finance/exports/ (whitelist enforced by pre-commit hook [3])
//   5. git commit -m "[auto-publish] finance/exports: <ts> by personal-finance"
//   6. if cfg.PublishPush, git push (otherwise stop after commit; M1 default)
//   7. on any failure: best-effort rollback (restore + remove unstaged files)
//      and append to ~/Library/Logs/personal/personal-finance.log
//
// Hard contract (publish-workflow.md §2.3 / §2.4):
//   - never write outside finance/exports/
//   - never force push, amend, or delete commits
//   - never edit files in the publish worktree by hand
package publish

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/walker-qiang/personal-finance/internal/config"
	"github.com/walker-qiang/personal-finance/internal/csvexport"
	"github.com/walker-qiang/personal-finance/internal/db/store"
)

// Result is what the HTTP handler returns to the caller and what we log.
type Result struct {
	OK              bool      `json:"ok"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at"`
	Worktree        string    `json:"worktree"`
	Branch          string    `json:"branch"`
	CommitSHA       string    `json:"commit_sha,omitempty"`
	FilesWritten    []string  `json:"files_written"`
	FilesStaged     []string  `json:"files_staged"`
	Pushed          bool      `json:"pushed"`
	NothingToCommit bool      `json:"nothing_to_commit"`
	Message         string    `json:"message,omitempty"`
	Error           string    `json:"error,omitempty"`
}

type Job struct {
	cfg   *config.Config
	store *store.Store
}

func New(cfg *config.Config, s *store.Store) *Job {
	return &Job{cfg: cfg, store: s}
}

// Run executes the publish flow synchronously. The caller (HTTP handler /
// CLI -publish-once) decides what to do with the Result.
func (j *Job) Run(ctx context.Context) (res Result) {
	res = Result{
		StartedAt: time.Now(),
		Worktree:  j.cfg.PublishWorktree,
	}
	defer func() { res.FinishedAt = time.Now() }()

	// 0. preflight
	if err := preflight(j.cfg.PublishWorktree); err != nil {
		res.Error = err.Error()
		j.logFailure(res)
		return res
	}

	// 1. sync to origin/main (publish-main is a thin proxy branch, see §2.3 step 1)
	if err := gitInWT(ctx, j.cfg.PublishWorktree, "fetch", "origin", "main"); err != nil {
		res.Error = "git fetch: " + err.Error()
		j.logFailure(res)
		return res
	}
	if err := gitInWT(ctx, j.cfg.PublishWorktree, "reset", "--hard", "origin/main"); err != nil {
		res.Error = "git reset --hard origin/main: " + err.Error()
		j.logFailure(res)
		return res
	}
	branch, err := gitOut(ctx, j.cfg.PublishWorktree, "rev-parse", "--abbrev-ref", "HEAD")
	if err == nil {
		res.Branch = strings.TrimSpace(branch)
	}

	// 2. assert clean (publish worktree must never have human edits)
	clean, err := isClean(ctx, j.cfg.PublishWorktree)
	if err != nil {
		res.Error = "git status: " + err.Error()
		j.logFailure(res)
		return res
	}
	if !clean {
		res.Error = "publish worktree is not clean after reset; aborting (someone edited it by hand?)"
		j.logFailure(res)
		return res
	}

	// 3. dump tables -> CSVs
	exportsDir := filepath.Join(j.cfg.PublishWorktree, "finance", "exports")
	if err := os.MkdirAll(exportsDir, 0o755); err != nil {
		res.Error = "mkdir exports: " + err.Error()
		j.logFailure(res)
		return res
	}
	stamp := res.StartedAt.Format("2006-01-02")
	files, err := j.dumpAll(ctx, exportsDir, stamp)
	if err != nil {
		res.Error = "dump tables: " + err.Error()
		j.rollback(ctx, files)
		j.logFailure(res)
		return res
	}
	res.FilesWritten = relativize(j.cfg.PublishWorktree, files)

	// 4. stage only the whitelist
	if err := gitInWT(ctx, j.cfg.PublishWorktree, append([]string{"add", "--"}, files...)...); err != nil {
		res.Error = "git add: " + err.Error()
		j.rollback(ctx, files)
		j.logFailure(res)
		return res
	}
	staged, err := stagedPaths(ctx, j.cfg.PublishWorktree)
	if err != nil {
		res.Error = "git diff --cached: " + err.Error()
		j.rollback(ctx, files)
		j.logFailure(res)
		return res
	}
	res.FilesStaged = staged

	if len(staged) == 0 {
		res.OK = true
		res.NothingToCommit = true
		res.Message = "no diff vs origin/main; skipped commit"
		return res
	}

	// 5. commit (pre-commit hook [3] will validate whitelist)
	msg := fmt.Sprintf("[auto-publish] finance/exports: %s by personal-finance", res.StartedAt.Format("2006-01-02 15:04:05"))
	if err := gitInWT(ctx, j.cfg.PublishWorktree, "commit", "-m", msg); err != nil {
		res.Error = "git commit: " + err.Error()
		j.rollback(ctx, files)
		j.logFailure(res)
		return res
	}
	sha, _ := gitOut(ctx, j.cfg.PublishWorktree, "rev-parse", "HEAD")
	res.CommitSHA = strings.TrimSpace(sha)

	// 6. push (optional)
	if j.cfg.PublishPush {
		if err := gitInWT(ctx, j.cfg.PublishWorktree, "push"); err != nil {
			res.Error = "git push: " + err.Error()
			j.logFailure(res)
			return res
		}
		res.Pushed = true
	}

	res.OK = true
	res.Message = "publish committed"
	if !res.Pushed {
		res.Message += " (no push; set PUBLISH_PUSH=1 to enable)"
	}
	return res
}

// --- internals ---

func preflight(worktree string) error {
	st, err := os.Stat(worktree)
	if err != nil {
		return fmt.Errorf("publish worktree %q not found: %w (see publish-workflow.md §2.1 to create it)", worktree, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("publish worktree %q is not a directory", worktree)
	}
	if _, err := os.Stat(filepath.Join(worktree, ".git")); err != nil {
		return fmt.Errorf("publish worktree %q has no .git (not a git worktree)", worktree)
	}
	return nil
}

func gitInWT(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w; output: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func isClean(ctx context.Context, dir string) (bool, error) {
	out, err := gitOut(ctx, dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}

func stagedPaths(ctx context.Context, dir string) ([]string, error) {
	out, err := gitOut(ctx, dir, "diff", "--cached", "--name-only")
	if err != nil {
		return nil, err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

func relativize(base string, abs []string) []string {
	rel := make([]string, 0, len(abs))
	for _, p := range abs {
		if r, err := filepath.Rel(base, p); err == nil {
			rel = append(rel, r)
		} else {
			rel = append(rel, p)
		}
	}
	return rel
}

// rollback is best-effort: unstage anything the job staged, restore tracked
// files, and remove untracked CSVs the job wrote. It must never make the
// worktree state worse than "clean on origin/main".
func (j *Job) rollback(ctx context.Context, abs []string) {
	_ = gitInWT(ctx, j.cfg.PublishWorktree, "restore", "--staged", ".")
	_ = gitInWT(ctx, j.cfg.PublishWorktree, "restore", ".")
	for _, p := range abs {
		_ = os.Remove(p)
	}
}

func (j *Job) logFailure(r Result) {
	f, err := os.OpenFile(j.cfg.LogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "[%s] publish FAILED branch=%s sha=%s err=%q\n",
		time.Now().Format(time.RFC3339), r.Branch, r.CommitSHA, r.Error)
}

// --- table dumpers ---
//
// CSV writer logic lives in internal/csvexport so that cmd/import's
// round-trip verifier can reuse the exact same code path. Any byte-level
// drift between publisher and verifier would silently break the DR
// guarantee, so the only allowed location for that code is csvexport.

func (j *Job) dumpAll(ctx context.Context, dir, stamp string) ([]string, error) {
	files := []string{
		filepath.Join(dir, "assets-"+stamp+".csv"),
		filepath.Join(dir, "snapshots-"+stamp+".csv"),
		filepath.Join(dir, "transactions-"+stamp+".csv"),
		filepath.Join(dir, "holdings-"+stamp+".csv"),
	}
	if err := csvexport.DumpAssets(ctx, j.store, files[0]); err != nil {
		return files, fmt.Errorf("assets: %w", err)
	}
	if err := csvexport.DumpSnapshots(ctx, j.store, files[1]); err != nil {
		return files, fmt.Errorf("snapshots: %w", err)
	}
	if err := csvexport.DumpTransactions(ctx, j.store, files[2]); err != nil {
		return files, fmt.Errorf("transactions: %w", err)
	}
	if err := csvexport.DumpHoldings(ctx, j.store, files[3]); err != nil {
		return files, fmt.Errorf("holdings: %w", err)
	}
	return files, nil
}
