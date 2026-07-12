package cmd

import (
	"os"
	"path/filepath"

	gomutant "github.com/greatliontech/gomutant"
)

func loadFindings(path string) ([]gomutant.Finding, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return gomutant.ParseFindings(data)
}

func findingsAt(dir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(dir, filepath.FromSlash(path))
}
