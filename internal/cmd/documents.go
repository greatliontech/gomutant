package cmd

import (
	"context"
	"path/filepath"

	gomutant "github.com/greatliontech/gomutant"
)

func loadFindings(dir, path string) ([]gomutant.Finding, error) {
	return loadFindingsContext(context.Background(), dir, path)
}

func loadFindingsContext(ctx context.Context, dir, path string) ([]gomutant.Finding, error) {
	store, err := gomutant.OpenStore(path, dir)
	if err != nil {
		return nil, err
	}
	findings, err := store.Load(ctx)
	if err != nil {
		return nil, err
	}
	return findings, ctx.Err()
}

func findingsAt(dir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(dir, filepath.FromSlash(path))
}
