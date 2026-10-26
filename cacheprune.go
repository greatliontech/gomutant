package gomutant

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/greatliontech/gofresh"
)

// CachePruneOptions configures PruneCacheContext: Check previews what
// would be reclaimed without deleting any files; AllTrees sweeps every
// tree's machine-local cache directory under the user cache root alongside
// the shared gofresh memo store; MaxBytes bounds retained baseline bank
// bytes per tree (0 reclaims all disposable baseline banks); MemoMaxBytes
// bounds retained gofresh memo bytes (negative uses MaxBytes);
// IncludeLocalRecords explicitly discards unattested machine-local overlay
// records; IncludeAttested (valid only with IncludeLocalRecords) also
// discards machine-local overlay records carrying human equivalence or
// withdrawal dispositions and preserved legacy records.
type CachePruneOptions struct {
	Check               bool
	AllTrees            bool
	FindingsPath        string
	MaxBytes            int64
	MemoMaxBytes        int64
	IncludeLocalRecords bool
	IncludeAttested     bool
}

// CachePruneResult reports the outcome (or check preview) of a cache
// cleanup across machine-local baseline banks, machine-local findings
// overlays, and the shared gofresh memo store.
type CachePruneResult struct {
	Check                   bool  `json:"check"`
	AllTrees                bool  `json:"allTrees,omitempty"`
	RemovedBaselines        int   `json:"removedBaselines"`
	ReclaimedBaselineBytes  int64 `json:"reclaimedBaselineBytes"`
	RetainedBaselines       int   `json:"retainedBaselines"`
	RetainedBaselineBytes   int64 `json:"retainedBaselineBytes"`
	RemovedLocalRecords     int   `json:"removedLocalRecords,omitempty"`
	ReclaimedLocalBytes     int64 `json:"reclaimedLocalBytes,omitempty"`
	RemovedAttestedRecords  int   `json:"removedAttestedRecords,omitempty"`
	RetainedLocalRecords    int   `json:"retainedLocalRecords"`
	RetainedLocalBytes      int64 `json:"retainedLocalBytes"`
	RetainedAttestedRecords int   `json:"retainedAttestedRecords"`
	RetainedAttestedBytes   int64 `json:"retainedAttestedBytes"`
	RetainedLegacyRecords   int   `json:"retainedLegacyRecords"`
	RetainedLegacyBytes     int64 `json:"retainedLegacyBytes"`
	RemovedCorruptEntries   int   `json:"removedCorruptEntries,omitempty"`
	ReclaimedCorruptBytes   int64 `json:"reclaimedCorruptBytes,omitempty"`
	SkippedActiveTrees      int   `json:"skippedActiveTrees,omitempty"`
	MemosRemoved            int   `json:"memosRemoved"`
	MemoReclaimedBytes      int64 `json:"memoReclaimedBytes"`
	MemoRetainedBytes       int64 `json:"memoRetainedBytes"`
}

// PruneCacheContext reclaims disposable baseline banks and gofresh analysis
// memos while preserving human-authored dispositions and legacy records in
// the machine-local findings overlay by default.
func PruneCacheContext(ctx context.Context, moduleDir string, opts CachePruneOptions) (CachePruneResult, error) {
	if err := ctx.Err(); err != nil {
		return CachePruneResult{}, err
	}
	if opts.IncludeAttested && !opts.IncludeLocalRecords {
		return CachePruneResult{}, errors.New("prune cache: include_attested requires include_local_records")
	}
	if opts.MaxBytes < 0 {
		return CachePruneResult{}, errors.New("prune cache: max_bytes must be non-negative")
	}
	res := CachePruneResult{
		Check:    opts.Check,
		AllTrees: opts.AllTrees,
	}
	var treeDirs []string
	_, currentTreeDir, err := machineLocalDir(moduleDir)
	if err != nil {
		return CachePruneResult{}, err
	}
	if opts.AllTrees {
		reposRoot := filepath.Dir(currentTreeDir)
		entries, readErr := os.ReadDir(reposRoot)
		if readErr != nil && !os.IsNotExist(readErr) {
			return CachePruneResult{}, readErr
		}
		for _, entry := range entries {
			if entry.IsDir() {
				treeDirs = append(treeDirs, filepath.Join(reposRoot, entry.Name()))
			}
		}
	} else {
		treeDirs = []string{currentTreeDir}
	}

	findingsPath := opts.FindingsPath
	if findingsPath == "" && moduleDir != "" {
		findingsPath = FindingsPathAt(moduleDir, "")
	}

	for _, treeDir := range treeDirs {
		if err := ctx.Err(); err != nil {
			return CachePruneResult{}, err
		}
		var releaseCampaign func()
		if treeDir == currentTreeDir && findingsPath != "" {
			if _, statErr := os.Stat(findingsPath + ".campaign"); statErr == nil {
				rel, lockErr := AcquireCampaignLock(findingsPath)
				if lockErr != nil {
					res.SkippedActiveTrees++
					continue
				}
				releaseCampaign = rel
			}
		}
		pruneErr := pruneTreeCacheDir(ctx, treeDir, opts, &res)
		if releaseCampaign != nil {
			releaseCampaign()
		}
		if pruneErr != nil {
			return CachePruneResult{}, pruneErr
		}
	}

	memoLimit := opts.MemoMaxBytes
	if memoLimit <= 0 && opts.MaxBytes > 0 {
		memoLimit = opts.MaxBytes
	}
	pruneMemos := gofresh.PruneMemos
	if opts.Check {
		pruneMemos = gofresh.PreviewPruneMemos
	}
	report, memoErr := pruneMemos(memoLimit)
	if memoErr != nil {
		return CachePruneResult{}, memoErr
	}
	res.MemosRemoved = report.RemovedEntries
	res.MemoReclaimedBytes = report.ReclaimedBytes
	res.MemoRetainedBytes = report.RemainingBytes
	return res, nil
}

func pruneTreeCacheDir(ctx context.Context, treeDir string, opts CachePruneOptions, res *CachePruneResult) error {
	if _, err := os.Stat(treeDir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}

	baselinePath := filepath.Join(treeDir, "baselines.json")
	if info, err := os.Stat(baselinePath); err == nil && !info.IsDir() {
		release, lockErr := acquireDocumentLock(ctx, baselinePath)
		if lockErr == nil {
			defer release()
		}
		size := info.Size()
		if opts.MaxBytes <= 0 || size > bankFileCeiling {
			res.RemovedBaselines++
			res.ReclaimedBaselineBytes += size
			if !opts.Check {
				_ = os.Remove(baselinePath)
			}
		} else {
			data, readErr := os.ReadFile(baselinePath)
			var file baselineBankFile
			if readErr != nil || json.Unmarshal(data, &file) != nil || (file.Version != bankVersion && file.Version != 2) {
				res.RemovedBaselines++
				res.ReclaimedBaselineBytes += size
				if !opts.Check {
					_ = os.Remove(baselinePath)
				}
			} else if size <= opts.MaxBytes {
				res.RetainedBaselines++
				res.RetainedBaselineBytes += size
			} else {
				marshaled, mErr := boundBankFileToBytes(&file, opts.MaxBytes)
				if mErr != nil || int64(len(marshaled)) > opts.MaxBytes || len(file.Baselines) == 0 && len(file.Coverage) == 0 {
					res.RemovedBaselines++
					res.ReclaimedBaselineBytes += size
					if !opts.Check {
						_ = os.Remove(baselinePath)
					}
				} else {
					reclaimed := size - int64(len(marshaled))
					if reclaimed > 0 {
						res.ReclaimedBaselineBytes += reclaimed
					}
					res.RetainedBaselines++
					res.RetainedBaselineBytes += int64(len(marshaled))
					if !opts.Check {
						_ = writeBoundedBankFile(baselinePath, marshaled)
					}
				}
			}
		}
	}

	overlayDir := filepath.Join(treeDir, "findings")
	entries, err := os.ReadDir(overlayDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".lock") || strings.HasSuffix(name, ".campaign") || strings.HasPrefix(name, ".entry-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		path := filepath.Join(overlayDir, name)
		info, statErr := os.Stat(path)
		if statErr != nil {
			continue
		}
		size := info.Size()
		if size > overlayEntryCeiling {
			res.RemovedCorruptEntries++
			res.ReclaimedCorruptBytes += size
			if !opts.Check {
				_ = os.Remove(path)
			}
			continue
		}
		if _, ok := sidelinedVersion(name); ok {
			if opts.IncludeLocalRecords && opts.IncludeAttested {
				res.RemovedLocalRecords++
				res.RemovedAttestedRecords++
				res.ReclaimedLocalBytes += size
				if !opts.Check {
					_ = os.Remove(path)
				}
			} else {
				res.RetainedLegacyRecords++
				res.RetainedLegacyBytes += size
			}
			continue
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		findings, parseErr := ParseFindings(data)
		if parseErr != nil || len(findings) != 1 {
			var versionErr *DocumentVersionError
			if errors.As(parseErr, &versionErr) {
				if opts.IncludeLocalRecords && opts.IncludeAttested {
					res.RemovedLocalRecords++
					res.RemovedAttestedRecords++
					res.ReclaimedLocalBytes += size
					if !opts.Check {
						_ = os.Remove(path)
					}
				} else {
					res.RetainedLegacyRecords++
					res.RetainedLegacyBytes += size
				}
				continue
			}
			res.RemovedCorruptEntries++
			res.ReclaimedCorruptBytes += size
			if !opts.Check {
				_ = os.Remove(path)
			}
			continue
		}
		f := findings[0]
		hasDispositions := len(f.Attested) > 0 || len(f.Withdrawn) > 0
		if !opts.IncludeLocalRecords || (hasDispositions && !opts.IncludeAttested) {
			res.RetainedLocalRecords++
			res.RetainedLocalBytes += size
			if hasDispositions {
				res.RetainedAttestedRecords++
				res.RetainedAttestedBytes += size
			}
			continue
		}
		res.RemovedLocalRecords++
		res.ReclaimedLocalBytes += size
		if hasDispositions {
			res.RemovedAttestedRecords++
		}
		if !opts.Check {
			_ = os.Remove(path)
		}
	}
	return nil
}

func boundBankFileToBytes(file *baselineBankFile, maxBytes int64) ([]byte, error) {
	if file.Version == 2 {
		file.Version = bankVersion
		for k, e := range file.Baselines {
			e.PassedTests = nil
			file.Baselines[k] = e
		}
	}
	data, err := json.Marshal(file)
	if err != nil {
		return nil, err
	}
	for int64(len(data)) > maxBytes && len(file.Baselines) > 0 {
		oldestKey := ""
		var oldestTime int64
		first := true
		for k, e := range file.Baselines {
			if first || e.MeasuredAtUnix < oldestTime || (e.MeasuredAtUnix == oldestTime && k < oldestKey) {
				oldestKey = k
				oldestTime = e.MeasuredAtUnix
				first = false
			}
		}
		delete(file.Baselines, oldestKey)
		data, err = json.Marshal(file)
		if err != nil {
			return nil, err
		}
	}
	return data, nil
}

func writeBoundedBankFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".baselines-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

