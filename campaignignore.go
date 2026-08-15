package gomutant

import (
	"os"
	"path/filepath"
	"strings"
)

// ensureCampaignIgnore keeps the by-design persistent campaign lock out
// of consumers' commits: inside the tool-owned .gomutant directory a
// minted .gitignore covers *.campaign, so an add-everything staging
// loop cannot commit a lock file whose persistence (the flock is the
// lock; the file deliberately outlives every campaign) is invisible
// from its name. A findings document outside the tool-owned directory
// keeps its directory untouched - minting ignore rules in user-owned
// directories is not the tool's call; the documented lock lifecycle
// covers that placement. Best-effort: hygiene never blocks a campaign.
func ensureCampaignIgnore(dir string) {
	if filepath.Base(dir) != ".gomutant" {
		return
	}
	path := filepath.Join(dir, ".gitignore")
	content, err := os.ReadFile(path)
	if err == nil && strings.Contains(string(content), "*.campaign") {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	if len(content) > 0 && !strings.HasSuffix(string(content), "\n") {
		_, _ = f.WriteString("\n")
	}
	_, _ = f.WriteString("*.campaign\n")
}
