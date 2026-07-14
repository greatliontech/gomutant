// Package mcpserver serves gomutant over the Model Context Protocol: the
// library's operations as tools, a thin shell exactly like the CLI so the two
// faces cannot drift (spec mcp.md). It inherits the advisory stance whole —
// no tool renders a pass/fail verdict (REQ-result-findings).
package mcpserver

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/contextio"
	"github.com/greatliontech/gomutant/internal/gitref"
)

// Server is a dir-bound MCP server over the gomutant library.
type Server struct {
	dir            string
	updateDocument func(context.Context, string, func([]gomutant.Finding) ([]gomutant.Finding, error)) error
}

// New builds a server rooted at dir.
func New(dir string) *Server {
	return &Server{dir: dir, updateDocument: gomutant.UpdateDocumentContext}
}

func (s *Server) update(ctx context.Context, path string, change func([]gomutant.Finding) ([]gomutant.Finding, error)) error {
	if s.updateDocument == nil {
		return gomutant.UpdateDocumentContext(ctx, path, change)
	}
	return s.updateDocument(ctx, path, change)
}

// Run serves MCP over stdio until the context ends.
func (s *Server) Run(ctx context.Context) error {
	return s.MCP().Run(ctx, &mcp.StdioTransport{})
}

// MCP builds the protocol server (REQ-mcp-tools).
func (s *Server) MCP() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "gomutant", Version: "v0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "run",
		Description: "Mutate the targets and run each one's oracle tests per mutant. Targets come from a document (gomutant's or stipulator's export), changed-scope discovery vs a git ref, or whole-tree discovery. Maintains the findings document: prior findings with matching pins are served, the rest re-measure. Survivors are findings awaiting disposition, never verdicts.",
	}, s.toolRun)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "discover",
		Description: "List effective target symbols, sorted opaque labels, explicit or package-derived oracle mode, skip reasons, and changed-scope residue. Exact oracles are deduplicated in top-level oracleSets; each target's oracleSet integer references oracleSets[].id.",
	}, s.toolDiscover)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "findings",
		Description: "Inspect every findings record as current, stale, unverifiable, or detached, with its open survivors and attested dispositions. Filter by opaque label; inspection runs no tests.",
	}, s.toolFindings)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "attest_survivor",
		Description: "Disposition a surviving mutant as equivalent, with the reasoning on record. Refused unless the mutant is among the finding's current survivors; shed automatically when any pin moves, so every body version is re-judged.",
	}, s.toolAttest)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "ephemeral",
		Description: "Run one manual mutant the operator set cannot generate: replace one file whole, apply sequential edits to one file, or apply an atomic exact-match edit batch across files, then check whether the named test kills it. The tree is never touched; the result is evidence, never persisted.",
	}, s.toolEphemeral)
	return srv
}

const defaultFindings = ".gomutant/findings.json"

func secondsDuration(name string, seconds int) (time.Duration, error) {
	const maxSeconds = int64((1<<63 - 1) / int64(time.Second))
	if seconds < 0 || int64(seconds) > maxSeconds {
		return 0, fmt.Errorf("%s is outside the supported duration range", name)
	}
	return time.Duration(seconds) * time.Second, nil
}

func (s *Server) findingsPath(override string) string {
	p := override
	if p == "" {
		p = defaultFindings
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(s.dir, filepath.FromSlash(p))
}

// localPath refuses a tree-relative input that escapes the server's dir —
// the surface is dir-bound, and an escaping ephemeral file would no-op in
// the overlay and read as a survivor.
func localPath(name, p string) error {
	if p == "" {
		return nil
	}
	drive := len(p) >= 2 && p[1] == ':' && ((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z'))
	if !strings.Contains(p, `\`) && !path.IsAbs(p) && !drive && path.Clean(p) == p && p != "." && !strings.HasPrefix(p, "../") {
		return nil
	}
	return fmt.Errorf("%s %q escapes the tree", name, p)
}

func (s *Server) loadFindings(override string) ([]gomutant.Finding, error) {
	return s.loadFindingsContext(context.Background(), override)
}

func (s *Server) loadFindingsContext(ctx context.Context, override string) ([]gomutant.Finding, error) {
	data, err := contextio.ReadFile(ctx, s.findingsPath(override))
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

type runIn struct {
	TargetsPath      string   `json:"targets_path,omitempty" jsonschema:"path to a targets document (gomutant's format or stipulator's export); overrides discovery"`
	TargetsJSON      string   `json:"targets_json,omitempty" jsonschema:"an inline targets document, same formats as targets_path"`
	Changed          string   `json:"changed,omitempty" jsonschema:"target only symbols whose bodies differ from this git ref (requires git)"`
	Budget           int      `json:"budget,omitempty" jsonschema:"candidates per symbol; 0 means exhaustive"`
	TimeoutSec       int      `json:"timeout_sec,omitempty" jsonschema:"cancel tool work before findings commit after this many seconds; 0 means unlimited"`
	OracleTimeoutSec int      `json:"oracle_timeout_sec,omitempty" jsonschema:"maximum duration of each oracle process in seconds; 0 means 60"`
	Jobs             int      `json:"jobs,omitempty" jsonschema:"concurrent mutant runs; 0 means half the CPUs"`
	Force            bool     `json:"force,omitempty" jsonschema:"re-measure targets whose prior finding still covers"`
	Findings         string   `json:"findings,omitempty" jsonschema:"findings document path (default .gomutant/findings.json), read and updated"`
	Packages         []string `json:"packages,omitempty" jsonschema:"complete package import-path glob filters; * stays within one slash component and ** as a complete component crosses components; alternatives"`
	Symbols          []string `json:"symbols,omitempty" jsonschema:"complete fully qualified symbol glob filters; * stays within one slash component and ** as a complete component crosses slash components, for example **/*emitConditions*; alternatives"`
}

type findingOut struct {
	Symbol         string                     `json:"symbol"`
	Labels         []string                   `json:"labels,omitempty"`
	CandidateCount int                        `json:"candidateCount"`
	Generated      int                        `json:"generated"`
	Mutants        int                        `json:"mutants"`
	Killed         int                        `json:"killed"`
	Discarded      int                        `json:"discarded"`
	Operators      []gomutant.OperatorSummary `json:"operators"`
	Attested       int                        `json:"attested,omitempty"`
	Open           []gomutant.Survivor        `json:"open,omitempty"`
	Cached         bool                       `json:"cached,omitempty"`
	Skipped        string                     `json:"skipped,omitempty"`
}

type runOut struct {
	Findings    []findingOut                `json:"findings"`
	Residue     []gomutant.Residue          `json:"residue,omitempty"`
	Preparation []gomutant.PreparationEvent `json:"preparation"`
	Decisions   []gomutant.RunDecision      `json:"decisions"`
	Summary     gomutant.RunSummary         `json:"summary"`
	Document    string                      `json:"document"`
}

func (s *Server) toolRun(ctx context.Context, req *mcp.CallToolRequest, in runIn) (result *mcp.CallToolResult, out runOut, err error) {
	commandTimeout, err := secondsDuration("timeout_sec", in.TimeoutSec)
	if err != nil {
		return nil, out, err
	}
	oracleTimeout, err := secondsDuration("oracle_timeout_sec", in.OracleTimeoutSec)
	if err != nil {
		return nil, out, err
	}
	if commandTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, commandTimeout)
		defer cancel()
	}
	defer func() {
		if err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				err = contextErr
			}
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, out, err
	}
	out.Preparation = append(out.Preparation, gomutant.PreparationEvent{Stage: gomutant.PreparationLoading})
	tree, err := gomutant.LoadContext(ctx, s.dir)
	if err != nil {
		return nil, out, err
	}
	var targets []gomutant.Target
	wholeTree := false
	forms := 0
	if in.TargetsPath != "" {
		forms++
	}
	if in.TargetsJSON != "" {
		forms++
	}
	if in.Changed != "" {
		forms++
	}
	if forms > 1 {
		return nil, out, fmt.Errorf("give targets_path, targets_json, or changed, at most one")
	}
	switch {
	case in.TargetsPath != "":
		if err := localPath("targets_path", in.TargetsPath); err != nil {
			return nil, out, err
		}
		data, err := contextio.ReadFile(ctx, filepath.Join(s.dir, filepath.FromSlash(in.TargetsPath)))
		if err != nil {
			return nil, out, err
		}
		if err := ctx.Err(); err != nil {
			return nil, out, err
		}
		if targets, err = gomutant.LoadTargetsContext(ctx, data); err != nil {
			return nil, out, err
		}
	case in.TargetsJSON != "":
		if targets, err = gomutant.LoadTargetsContext(ctx, []byte(in.TargetsJSON)); err != nil {
			return nil, out, err
		}
	case in.Changed != "":
		paths, err := gitref.ChangedPathsContext(ctx, s.dir, in.Changed)
		if err != nil {
			return nil, out, err
		}
		targets, out.Residue, err = tree.DiscoverChangedContext(ctx, paths, func(p string) ([]byte, bool) {
			return gitref.ShowContext(ctx, s.dir, in.Changed, p)
		})
		if err != nil {
			return nil, out, err
		}
	default:
		targets, err = tree.DiscoverContext(ctx)
		if err != nil {
			return nil, out, err
		}
		wholeTree = true
	}
	targets, err = tree.FilterTargetsContext(ctx, targets, in.Packages, in.Symbols)
	if err != nil {
		return nil, out, err
	}
	if len(in.Packages) != 0 || len(in.Symbols) != 0 {
		wholeTree = false
	}
	if len(targets) == 0 {
		if err := ctx.Err(); err != nil {
			return nil, out, err
		}
		out.Document = s.findingsPath(in.Findings)
		if wholeTree {
			err := s.update(ctx, out.Document, func(current []gomutant.Finding) ([]gomutant.Finding, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				return gomutant.MergeWholeFindings(current, nil, nil), nil
			})
			if err != nil {
				return nil, out, err
			}
		}
		return nil, out, nil
	}
	prior, err := s.loadFindingsContext(ctx, in.Findings)
	if err != nil {
		return nil, out, err
	}
	if err := ctx.Err(); err != nil {
		return nil, out, err
	}
	findings, err := tree.Run(ctx, targets, gomutant.Options{
		Budget:        in.Budget,
		OracleTimeout: oracleTimeout,
		Jobs:          in.Jobs,
		Force:         in.Force,
		Prior:         prior,
		Decision:      func(decision gomutant.RunDecision) { out.Decisions = append(out.Decisions, decision) },
		Progress:      func(event gomutant.PreparationEvent) { out.Preparation = append(out.Preparation, event) },
	})
	if err != nil {
		return nil, out, err
	}
	out.Summary = gomutant.SummarizeRun(findings)
	for _, f := range findings {
		if err := ctx.Err(); err != nil {
			return nil, out, err
		}
		out.Findings = append(out.Findings, findingOut{
			Symbol: f.Symbol, Labels: f.Labels,
			CandidateCount: f.CandidateCount, Generated: f.Generated,
			Mutants: f.Mutants, Killed: f.Killed, Discarded: f.Discarded,
			Attested: len(f.Attested), Operators: append([]gomutant.OperatorSummary{}, f.Operators...), Open: f.Open(),
			Cached: f.Cached, Skipped: f.Skipped,
		})
	}
	err = s.update(ctx, s.findingsPath(in.Findings), func(current []gomutant.Finding) ([]gomutant.Finding, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if wholeTree {
			return gomutant.MergeWholeFindings(current, findings, targets), nil
		}
		return gomutant.MergeFindings(current, findings), nil
	})
	if err != nil {
		return nil, out, err
	}
	out.Document = s.findingsPath(in.Findings)
	return nil, out, nil
}

type discoverIn struct {
	TargetsPath string   `json:"targets_path,omitempty" jsonschema:"path to a targets document; overrides discovery"`
	TargetsJSON string   `json:"targets_json,omitempty" jsonschema:"inline targets document; overrides discovery"`
	Changed     string   `json:"changed,omitempty" jsonschema:"changed-scope vs this git ref; empty means the whole tree"`
	Packages    []string `json:"packages,omitempty" jsonschema:"complete package import-path glob filters; * stays within one slash component and ** as a complete component crosses components; alternatives"`
	Symbols     []string `json:"symbols,omitempty" jsonschema:"complete fully qualified symbol glob filters; * stays within one slash component and ** as a complete component crosses slash components, for example **/*emitConditions*; alternatives"`
}

type discoverTarget struct {
	Symbol         string   `json:"symbol" jsonschema:"fully qualified target symbol"`
	OracleSet      int      `json:"oracleSet" jsonschema:"zero-based id that references one entry in the top-level oracleSets array"`
	Labels         []string `json:"labels,omitempty" jsonschema:"sorted opaque labels carried unchanged from the target"`
	OracleExplicit bool     `json:"oracleExplicit" jsonschema:"whether the referenced oracle was explicitly supplied rather than package-derived"`
	Skipped        string   `json:"skipped,omitempty" jsonschema:"reason this target cannot be measured, when applicable"`
}

type discoverOracleSet struct {
	ID     int      `json:"id" jsonschema:"zero-based oracle-set id referenced by targets[].oracleSet"`
	Oracle []string `json:"oracle" jsonschema:"sorted fully qualified test symbols forming this exact effective oracle"`
}

type discoverOut struct {
	OracleSets []discoverOracleSet `json:"oracleSets" jsonschema:"canonical exact oracle sets assigned in first-target order"`
	Targets    []discoverTarget    `json:"targets" jsonschema:"ordered effective targets whose oracleSet references oracleSets[].id"`
	Residue    []gomutant.Residue  `json:"residue,omitempty"`
}

func (s *Server) toolDiscover(ctx context.Context, req *mcp.CallToolRequest, in discoverIn) (*mcp.CallToolResult, discoverOut, error) {
	var out discoverOut
	tree, err := gomutant.LoadContext(ctx, s.dir)
	if err != nil {
		return nil, out, err
	}
	forms := 0
	if in.TargetsPath != "" {
		forms++
	}
	if in.TargetsJSON != "" {
		forms++
	}
	if in.Changed != "" {
		forms++
	}
	if forms > 1 {
		return nil, out, fmt.Errorf("give targets_path, targets_json, or changed, at most one")
	}
	var targets []gomutant.Target
	switch {
	case in.TargetsPath != "":
		if err := localPath("targets_path", in.TargetsPath); err != nil {
			return nil, out, err
		}
		data, err := contextio.ReadFile(ctx, filepath.Join(s.dir, filepath.FromSlash(in.TargetsPath)))
		if err != nil {
			return nil, out, err
		}
		if err := ctx.Err(); err != nil {
			return nil, out, err
		}
		targets, err = gomutant.LoadTargetsContext(ctx, data)
		if err != nil {
			return nil, out, err
		}
	case in.TargetsJSON != "":
		targets, err = gomutant.LoadTargetsContext(ctx, []byte(in.TargetsJSON))
		if err != nil {
			return nil, out, err
		}
	case in.Changed != "":
		paths, err := gitref.ChangedPathsContext(ctx, s.dir, in.Changed)
		if err != nil {
			return nil, out, err
		}
		targets, out.Residue, err = tree.DiscoverChangedContext(ctx, paths, func(p string) ([]byte, bool) {
			return gitref.ShowContext(ctx, s.dir, in.Changed, p)
		})
		if err != nil {
			return nil, out, err
		}
	default:
		targets, err = tree.DiscoverContext(ctx)
		if err != nil {
			return nil, out, err
		}
	}
	targets, err = tree.FilterTargetsContext(ctx, targets, in.Packages, in.Symbols)
	if err != nil {
		return nil, out, err
	}
	descriptions, err := tree.DescribeTargetsContext(ctx, targets)
	if err != nil {
		return nil, out, err
	}
	out.OracleSets, out.Targets = compactTargetDescriptions(descriptions)
	return nil, out, nil
}

func compactTargetDescriptions(descriptions []gomutant.TargetDescription) ([]discoverOracleSet, []discoverTarget) {
	sets := make([]discoverOracleSet, 0)
	setByKey := map[string]int{}
	targets := make([]discoverTarget, 0, len(descriptions))
	for _, description := range descriptions {
		var key strings.Builder
		for _, oracle := range description.Oracle {
			fmt.Fprintf(&key, "%d:", len(oracle))
			key.WriteString(oracle)
		}
		id, ok := setByKey[key.String()]
		if !ok {
			id = len(sets)
			setByKey[key.String()] = id
			sets = append(sets, discoverOracleSet{ID: id, Oracle: description.Oracle})
		}
		targets = append(targets, discoverTarget{
			Symbol: description.Symbol, OracleSet: id, Labels: description.Labels,
			OracleExplicit: description.OracleExplicit, Skipped: description.Skipped,
		})
	}
	return sets, targets
}

type findingsIn struct {
	Label    string `json:"label,omitempty" jsonschema:"show only findings carrying this label"`
	Findings string `json:"findings,omitempty" jsonschema:"findings document path (default .gomutant/findings.json)"`
}

type inspectedFinding struct {
	Symbol         string                     `json:"symbol"`
	Labels         []string                   `json:"labels,omitempty"`
	State          gomutant.FindingState      `json:"state"`
	Reason         string                     `json:"reason,omitempty"`
	CandidateCount int                        `json:"candidateCount"`
	Generated      int                        `json:"generated"`
	Mutants        int                        `json:"mutants"`
	Killed         int                        `json:"killed"`
	Discarded      int                        `json:"discarded"`
	Operators      []gomutant.OperatorSummary `json:"operators"`
	Open           []gomutant.Survivor        `json:"open"`
	Attested       []gomutant.Attestation     `json:"attested"`
}

type findingsOut struct {
	Findings []inspectedFinding `json:"findings"`
}

func (s *Server) toolFindings(ctx context.Context, req *mcp.CallToolRequest, in findingsIn) (*mcp.CallToolResult, findingsOut, error) {
	out := findingsOut{Findings: []inspectedFinding{}}
	all, err := s.loadFindingsContext(ctx, in.Findings)
	if err != nil {
		return nil, out, err
	}
	if err := ctx.Err(); err != nil {
		return nil, out, err
	}
	if len(all) == 0 {
		return nil, out, nil
	}
	tree, err := gomutant.LoadContext(ctx, s.dir)
	if err != nil {
		return nil, out, err
	}
	for _, finding := range all {
		if err := ctx.Err(); err != nil {
			return nil, out, err
		}
		if in.Label != "" && !containsLabel(finding.Labels, in.Label) {
			continue
		}
		inspection, err := tree.InspectFindingContext(ctx, finding)
		if err != nil {
			return nil, out, err
		}
		labels := append([]string(nil), finding.Labels...)
		sort.Strings(labels)
		out.Findings = append(out.Findings, inspectedFinding{
			Symbol: finding.Symbol, Labels: labels, State: inspection.State, Reason: inspection.Reason,
			CandidateCount: finding.CandidateCount, Generated: finding.Generated,
			Mutants: finding.Mutants, Killed: finding.Killed, Discarded: finding.Discarded,
			Operators: append([]gomutant.OperatorSummary{}, finding.Operators...),
			Open:      append([]gomutant.Survivor{}, finding.Open()...), Attested: append([]gomutant.Attestation{}, finding.AttestedDispositions()...),
		})
	}
	sort.Slice(out.Findings, func(i, j int) bool { return out.Findings[i].Symbol < out.Findings[j].Symbol })
	return nil, out, nil
}

func containsLabel(labels []string, want string) bool {
	for _, label := range labels {
		if label == want {
			return true
		}
	}
	return false
}

type attestIn struct {
	Symbol   string `json:"symbol" jsonschema:"the mutated symbol"`
	Position string `json:"position" jsonschema:"the survivor's position (file.go:line:col), as reported"`
	Operator string `json:"operator" jsonschema:"the survivor's operator, as reported"`
	Reason   string `json:"reason" jsonschema:"why the mutant is equivalent"`
	Findings string `json:"findings,omitempty" jsonschema:"findings document path (default .gomutant/findings.json)"`
}

type attestOut struct {
	Open int `json:"open" jsonschema:"the symbol's open findings after the disposition"`
}

func (s *Server) toolAttest(ctx context.Context, req *mcp.CallToolRequest, in attestIn) (*mcp.CallToolResult, attestOut, error) {
	var out attestOut
	for _, f := range map[string]string{"symbol": in.Symbol, "position": in.Position, "operator": in.Operator, "reason": in.Reason} {
		if f == "" {
			return nil, out, fmt.Errorf("attest_survivor needs symbol, position, operator, and reason")
		}
	}
	err := s.update(ctx, s.findingsPath(in.Findings), func(all []gomutant.Finding) ([]gomutant.Finding, error) {
		for i := range all {
			if all[i].Symbol == in.Symbol {
				if err := all[i].Attest(in.Position, in.Operator, in.Reason); err != nil {
					return nil, err
				}
				out.Open = len(all[i].Open())
				return all, nil
			}
		}
		return nil, fmt.Errorf("no finding for %s", in.Symbol)
	})
	return nil, out, err
}

type ephemeralIn struct {
	File             string               `json:"file,omitempty" jsonschema:"tree-relative source file for replacement or edits; omit for batch_edits"`
	Replacement      string               `json:"replacement,omitempty" jsonschema:"the whole replacement source; give exactly one mutation form"`
	Edits            []gomutant.Edit      `json:"edits,omitempty" jsonschema:"exact-match edits applied sequentially — each old must match exactly once in the content the prior edits produced; state the change, not the file"`
	BatchEdits       []gomutant.BatchEdit `json:"batch_edits,omitempty" jsonschema:"atomic file-scoped exact-match edits; every match resolves against the original file snapshot"`
	TestPkg          string               `json:"test_pkg" jsonschema:"go package path whose named test decides the kill"`
	Run              string               `json:"run" jsonschema:"-run pattern naming the deciding test"`
	TimeoutSec       int                  `json:"timeout_sec,omitempty" jsonschema:"cancel tool work before attributed result completion after this many seconds; 0 means unlimited"`
	OracleTimeoutSec int                  `json:"oracle_timeout_sec,omitempty" jsonschema:"maximum duration of each oracle process in seconds; 0 means 60"`
}

func (s *Server) toolEphemeral(ctx context.Context, req *mcp.CallToolRequest, in ephemeralIn) (*mcp.CallToolResult, *gomutant.EphemeralResult, error) {
	commandTimeout, err := secondsDuration("timeout_sec", in.TimeoutSec)
	if err != nil {
		return nil, nil, err
	}
	oracleTimeout, err := secondsDuration("oracle_timeout_sec", in.OracleTimeoutSec)
	if err != nil {
		return nil, nil, err
	}
	if commandTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, commandTimeout)
		defer cancel()
	}
	if in.TestPkg == "" || in.Run == "" {
		return nil, nil, fmt.Errorf("ephemeral needs test_pkg and run")
	}
	forms := 0
	if in.Replacement != "" {
		forms++
	}
	if len(in.Edits) != 0 {
		forms++
	}
	if len(in.BatchEdits) != 0 {
		forms++
	}
	if forms != 1 {
		return nil, nil, fmt.Errorf("give replacement, edits, or batch_edits, exactly one")
	}
	if len(in.BatchEdits) == 0 {
		if in.File == "" {
			return nil, nil, fmt.Errorf("replacement and edits need file")
		}
		if err := localPath("file", in.File); err != nil {
			return nil, nil, err
		}
	} else {
		if in.File != "" {
			return nil, nil, fmt.Errorf("batch_edits carries its own files; omit file")
		}
		for i, edit := range in.BatchEdits {
			if err := localPath(fmt.Sprintf("batch_edits[%d].file", i), edit.File); err != nil {
				return nil, nil, err
			}
		}
	}
	tree, err := gomutant.LoadContext(ctx, s.dir)
	if err != nil {
		return nil, nil, err
	}
	if len(in.BatchEdits) > 0 {
		res, err := tree.EphemeralBatch(ctx, in.BatchEdits, in.TestPkg, in.Run, oracleTimeout)
		return nil, res, err
	}
	if len(in.Edits) > 0 {
		res, err := tree.EphemeralEdits(ctx, in.File, in.Edits, in.TestPkg, in.Run, oracleTimeout)
		return nil, res, err
	}
	res, err := tree.Ephemeral(ctx, in.File, []byte(in.Replacement), in.TestPkg, in.Run, oracleTimeout)
	return nil, res, err
}
