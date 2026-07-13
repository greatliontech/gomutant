package cmd

import (
	"context"
	"os"
	"path/filepath"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/contextio"
)

func loadFindings(path string) ([]gomutant.Finding, error) {
	return loadFindingsContext(context.Background(), path)
}

func loadFindingsContext(ctx context.Context, path string) ([]gomutant.Finding, error) {
	data, err := contextio.ReadFile(ctx, path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	findings, err := gomutant.ParseFindings(data)
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
