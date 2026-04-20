package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/walker-qiang/personal-finance/internal/csvexport"
	"github.com/walker-qiang/personal-finance/internal/db/store"
)

// roundTripVerify dumps all 4 tables (assets, snapshots, transactions, holdings)
// to a temp directory using the SAME CSV writer (internal/csvexport) that
// publish.go uses, then SHA-256 compares each output against the source file.
// Any mismatch = the import path lost or mutated information.
//
// Why holdings is checked even though it's a VIEW: the view derives from
// snapshots+assets, so if assets and snapshots round-tripped cleanly the
// holdings dump must too. A mismatch here is a much louder signal than a
// snapshots mismatch alone, because it means JOIN/ORDER are also stable.

func roundTripVerify(ctx context.Context, conn *sql.DB, sourceDir, date string) error {
	tmpDir, err := os.MkdirTemp("", "finance-verify-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	st := store.New(conn)
	files := []struct {
		name string
		dump func(string) error
	}{
		{"assets-" + date + ".csv", func(p string) error { return csvexport.DumpAssets(ctx, st, p) }},
		{"snapshots-" + date + ".csv", func(p string) error { return csvexport.DumpSnapshots(ctx, st, p) }},
		{"transactions-" + date + ".csv", func(p string) error { return csvexport.DumpTransactions(ctx, st, p) }},
		{"holdings-" + date + ".csv", func(p string) error { return csvexport.DumpHoldings(ctx, st, p) }},
	}

	var diffs []string
	for _, f := range files {
		dst := filepath.Join(tmpDir, f.name)
		if err := f.dump(dst); err != nil {
			return fmt.Errorf("dump %s: %w", f.name, err)
		}
		got, err := sha256File(dst)
		if err != nil {
			return fmt.Errorf("hash %s: %w", dst, err)
		}
		srcPath := filepath.Join(sourceDir, f.name)
		want, err := sha256File(srcPath)
		if errors.Is(err, os.ErrNotExist) {
			// Source file may legitimately be missing (e.g. transactions-*.csv on
			// a fresh import). Skip the diff for that file.
			continue
		}
		if err != nil {
			return fmt.Errorf("hash %s: %w", srcPath, err)
		}
		if got != want {
			diffs = append(diffs, fmt.Sprintf("  %s: got=%s want=%s", f.name, got[:12], want[:12]))
			if d := firstDiff(srcPath, dst); d != "" {
				diffs = append(diffs, "    first diff: "+d)
			}
		}
	}
	if len(diffs) > 0 {
		return fmt.Errorf("round-trip mismatch:\n%s\nhint: if publish format changed, the divergence is in internal/csvexport (single source of truth)", joinLines(diffs))
	}
	return nil
}

func sha256File(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// firstDiff returns a short description of the first byte where the two
// files differ. Used purely for human-friendly error output.
func firstDiff(a, b string) string {
	ab, errA := os.ReadFile(a)
	bb, errB := os.ReadFile(b)
	if errA != nil || errB != nil {
		return ""
	}
	min := len(ab)
	if len(bb) < min {
		min = len(bb)
	}
	for i := 0; i < min; i++ {
		if ab[i] != bb[i] {
			return fmt.Sprintf("byte %d (line ~%d): %q vs %q", i, 1+bytes.Count(ab[:i], []byte{'\n'}), nearby(ab, i), nearby(bb, i))
		}
	}
	if len(ab) != len(bb) {
		return fmt.Sprintf("size %d vs %d (one file is a prefix of the other)", len(ab), len(bb))
	}
	return ""
}

func nearby(buf []byte, i int) string {
	start := i - 20
	if start < 0 {
		start = 0
	}
	end := i + 20
	if end > len(buf) {
		end = len(buf)
	}
	return string(buf[start:end])
}

func joinLines(xs []string) string {
	out := ""
	for i, s := range xs {
		if i > 0 {
			out += "\n"
		}
		out += s
	}
	return out
}
