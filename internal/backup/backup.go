package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"skarb/internal/store"
)

// Create writes a consistent SQLite snapshot using VACUUM INTO.
func Create(ctx context.Context, st *store.Store, dir string, retainDays int) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := "skarb-" + time.Now().UTC().Format("20060102") + ".db"
	target := filepath.Join(dir, name)
	if _, err := os.Stat(target); err == nil {
		return target, nil
	}
	stmt := "VACUUM INTO '" + strings.ReplaceAll(target, "'", "''") + "'"
	if _, err := st.DB.ExecContext(ctx, stmt); err != nil {
		return "", fmt.Errorf("vacuum into backup: %w", err)
	}
	if err := os.Chmod(target, 0o600); err != nil {
		return "", err
	}
	prune(dir, retainDays)
	return target, nil
}

func prune(dir string, retainDays int) {
	if retainDays <= 0 {
		retainDays = 14
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -retainDays)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "skarb-") || !strings.HasSuffix(entry.Name(), ".db") {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, entry.Name()))
	}
}
