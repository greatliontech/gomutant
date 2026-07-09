// Package mcpserver serves gomutant over the Model Context Protocol: the
// library's operations as tools, a thin shell exactly like the CLI so the two
// faces cannot drift (spec mcp.md). It inherits the advisory stance whole —
// no tool renders a pass/fail verdict (REQ-result-findings).
package mcpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/gitref"
)

// Server is a dir-bound MCP server over the gomutant library.
type Server struct {
	dir string
}

// New builds a server rooted at dir.
func New(dir string) *Server { return &Server{dir: dir} }

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
		Description: "List the targets a run would measure — whole tree or changed-scope vs a git ref — plus, in changed scope, the residue: every changed-but-untargeted path with the reason it yields no target.",
	}, s.toolDiscover)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "findings",
		Description: "Open findings (survivors minus attested dispositions) from the findings document, grouped by label. A finding means the tests vouching for that symbol did not notice the mutation: strengthen a test or attest an equivalence.",
	}, s.toolFindings)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "attest_survivor",
		Description: "Disposition a surviving mutant as equivalent, with the reasoning on record. Refused unless the mutant is among the finding's current survivors; shed automatically when any pin moves, so every body version is re-judged.",
	}, s.toolAttest)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "ephemeral",
		Description: "Run one manual mutant the operator set cannot generate: replace a file's content (whole, or as exact-match edits — state the change, not the file) and check whether the named test kills it. The tree is never touched; the result is evidence, never persisted.",
	}, s.toolEphemeral)
	return srv
}

const defaultFindings = ".gomutant/findings.json"

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
	if p == "" || filepath.IsLocal(filepath.FromSlash(p)) {
		return nil
	}
	return fmt.Errorf("%s %q escapes the tree", name, p)
}

func (s *Server) loadFindings(override string) ([]gomutant.Finding, error) {
	data, err := os.ReadFile(s.findingsPath(override))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return gomutant.ParseFindings(data)
}

type runIn struct {
	TargetsPath string `json:"targets_path,omitempty" jsonschema:"path to a targets document (gomutant's format or stipulator's export); overrides discovery"`
	TargetsJSON string `json:"targets_json,omitempty" jsonschema:"an inline targets document, same formats as targets_path"`
	Changed     string `json:"changed,omitempty" jsonschema:"target only symbols whose bodies differ from this git ref (requires git)"`
	Budget      int    `json:"budget,omitempty" jsonschema:"mutants per symbol; 0 means exhaustive"`
	TimeoutSec  int    `json:"timeout_sec,omitempty" jsonschema:"one mutant's oracle-run budget in seconds; 0 means 60"`
	Jobs        int    `json:"jobs,omitempty" jsonschema:"concurrent mutant runs; 0 means half the CPUs"`
	Force       bool   `json:"force,omitempty" jsonschema:"re-measure targets whose prior finding still covers"`
	Findings    string `json:"findings,omitempty" jsonschema:"findings document path (default .gomutant/findings.json), read and updated"`
}

type findingOut struct {
	Symbol    string              `json:"symbol"`
	Labels    []string            `json:"labels,omitempty"`
	Mutants   int                 `json:"mutants"`
	Killed    int                 `json:"killed"`
	Discarded int                 `json:"discarded,omitempty"`
	Attested  int                 `json:"attested,omitempty"`
	Open      []gomutant.Survivor `json:"open,omitempty"`
	Cached    bool                `json:"cached,omitempty"`
	Skipped   string              `json:"skipped,omitempty"`
}

type runOut struct {
	Findings []findingOut       `json:"findings"`
	Residue  []gomutant.Residue `json:"residue,omitempty"`
	Document string             `json:"document"`
}

func (s *Server) toolRun(ctx context.Context, req *mcp.CallToolRequest, in runIn) (*mcp.CallToolResult, runOut, error) {
	var out runOut
	tree, err := gomutant.Load(s.dir)
	if err != nil {
		return nil, out, err
	}
	var targets []gomutant.Target
	switch {
	case in.TargetsPath != "" && in.TargetsJSON != "":
		return nil, out, fmt.Errorf("give targets_path or targets_json, not both")
	case in.TargetsPath != "":
		if err := localPath("targets_path", in.TargetsPath); err != nil {
			return nil, out, err
		}
		data, err := os.ReadFile(filepath.Join(s.dir, filepath.FromSlash(in.TargetsPath)))
		if err != nil {
			return nil, out, err
		}
		if targets, err = gomutant.LoadTargets(data); err != nil {
			return nil, out, err
		}
	case in.TargetsJSON != "":
		if targets, err = gomutant.LoadTargets([]byte(in.TargetsJSON)); err != nil {
			return nil, out, err
		}
	case in.Changed != "":
		paths, err := gitref.ChangedPaths(s.dir, in.Changed)
		if err != nil {
			return nil, out, err
		}
		targets, out.Residue = tree.DiscoverChanged(paths, func(p string) ([]byte, bool) {
			return gitref.Show(s.dir, in.Changed, p)
		})
	default:
		targets = tree.Discover()
	}
	if len(targets) == 0 {
		out.Document = s.findingsPath(in.Findings)
		return nil, out, nil
	}
	prior, err := s.loadFindings(in.Findings)
	if err != nil {
		return nil, out, err
	}
	findings, err := tree.Run(ctx, targets, gomutant.Options{
		Budget:  in.Budget,
		Timeout: time.Duration(in.TimeoutSec) * time.Second,
		Jobs:    in.Jobs,
		Force:   in.Force,
		Prior:   prior,
	})
	if err != nil {
		return nil, out, err
	}
	for _, f := range findings {
		out.Findings = append(out.Findings, findingOut{
			Symbol: f.Symbol, Labels: f.Labels,
			Mutants: f.Mutants, Killed: f.Killed, Discarded: f.Discarded,
			Attested: len(f.Attested), Open: f.Open(),
			Cached: f.Cached, Skipped: f.Skipped,
		})
	}
	// A scoped run never drops the rest of the document; the merge re-reads
	// under the lock, so a disposition landing mid-run is never clobbered
	// (REQ-mcp-findings-doc).
	err = gomutant.UpdateDocument(s.findingsPath(in.Findings), func(current []gomutant.Finding) ([]gomutant.Finding, error) {
		return gomutant.MergeFindings(current, findings), nil
	})
	if err != nil {
		return nil, out, err
	}
	out.Document = s.findingsPath(in.Findings)
	return nil, out, nil
}

type discoverIn struct {
	Changed string `json:"changed,omitempty" jsonschema:"changed-scope vs this git ref; empty means the whole tree"`
}

type discoverOut struct {
	Targets []gomutant.Target  `json:"targets"`
	Residue []gomutant.Residue `json:"residue,omitempty"`
}

func (s *Server) toolDiscover(ctx context.Context, req *mcp.CallToolRequest, in discoverIn) (*mcp.CallToolResult, discoverOut, error) {
	var out discoverOut
	tree, err := gomutant.Load(s.dir)
	if err != nil {
		return nil, out, err
	}
	if in.Changed == "" {
		out.Targets = tree.Discover()
		return nil, out, nil
	}
	paths, err := gitref.ChangedPaths(s.dir, in.Changed)
	if err != nil {
		return nil, out, err
	}
	out.Targets, out.Residue = tree.DiscoverChanged(paths, func(p string) ([]byte, bool) {
		return gitref.Show(s.dir, in.Changed, p)
	})
	return nil, out, nil
}

type findingsIn struct {
	Label    string `json:"label,omitempty" jsonschema:"show only findings carrying this label"`
	Findings string `json:"findings,omitempty" jsonschema:"findings document path (default .gomutant/findings.json)"`
}

type openFinding struct {
	Symbol   string `json:"symbol"`
	Position string `json:"position"`
	Operator string `json:"operator"`
}

type findingsOut struct {
	ByLabel map[string][]openFinding `json:"byLabel"`
}

func (s *Server) toolFindings(ctx context.Context, req *mcp.CallToolRequest, in findingsIn) (*mcp.CallToolResult, findingsOut, error) {
	out := findingsOut{ByLabel: map[string][]openFinding{}}
	all, err := s.loadFindings(in.Findings)
	if err != nil {
		return nil, out, err
	}
	for _, f := range all {
		open := f.Open()
		if len(open) == 0 {
			continue
		}
		labels := f.Labels
		if len(labels) == 0 {
			labels = []string{"(unlabeled)"}
		}
		for _, l := range labels {
			if in.Label != "" && l != in.Label {
				continue
			}
			for _, sv := range open {
				out.ByLabel[l] = append(out.ByLabel[l], openFinding{Symbol: f.Symbol, Position: sv.Position, Operator: sv.Operator})
			}
		}
	}
	for _, group := range out.ByLabel {
		sort.Slice(group, func(i, j int) bool {
			if group[i].Symbol != group[j].Symbol {
				return group[i].Symbol < group[j].Symbol
			}
			return group[i].Position < group[j].Position
		})
	}
	return nil, out, nil
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
	err := gomutant.UpdateDocument(s.findingsPath(in.Findings), func(all []gomutant.Finding) ([]gomutant.Finding, error) {
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
	File        string          `json:"file" jsonschema:"tree-relative source file to mutate"`
	Replacement string          `json:"replacement,omitempty" jsonschema:"the whole replacement source; give this or edits"`
	Edits       []gomutant.Edit `json:"edits,omitempty" jsonschema:"exact-match edits applied sequentially — each old must match exactly once in the content the prior edits produced; state the change, not the file"`
	TestPkg     string          `json:"test_pkg" jsonschema:"go package path whose named test decides the kill"`
	Run         string          `json:"run" jsonschema:"-run pattern naming the deciding test"`
	TimeoutSec  int             `json:"timeout_sec,omitempty" jsonschema:"run budget in seconds; 0 means 60"`
}

func (s *Server) toolEphemeral(ctx context.Context, req *mcp.CallToolRequest, in ephemeralIn) (*mcp.CallToolResult, *gomutant.EphemeralResult, error) {
	if in.File == "" || in.TestPkg == "" || in.Run == "" {
		return nil, nil, fmt.Errorf("ephemeral needs file, test_pkg, and run")
	}
	if err := localPath("file", in.File); err != nil {
		return nil, nil, err
	}
	if (in.Replacement == "") == (len(in.Edits) == 0) {
		return nil, nil, fmt.Errorf("give replacement or edits, exactly one")
	}
	tree, err := gomutant.Load(s.dir)
	if err != nil {
		return nil, nil, err
	}
	timeout := time.Duration(in.TimeoutSec) * time.Second
	if len(in.Edits) > 0 {
		res, err := tree.EphemeralEdits(ctx, in.File, in.Edits, in.TestPkg, in.Run, timeout)
		return nil, res, err
	}
	res, err := tree.Ephemeral(ctx, in.File, []byte(in.Replacement), in.TestPkg, in.Run, timeout)
	return nil, res, err
}
