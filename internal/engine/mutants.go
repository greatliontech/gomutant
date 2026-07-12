package engine

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"path/filepath"
	"strconv"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/imports"
)

// Replacement is one original file and its complete overlaid content.
type Replacement struct {
	File   string
	Source []byte
}

// Mutant is one mutation represented by every file replacement that must be
// visible atomically during its overlay run.
type Mutant struct {
	Symbol       string
	Operator     string
	Position     string
	Replacements []Replacement
}

// OperatorSet identifies the mutant-generation basis; finding records pin
// it, so extending the operator set re-stales every record — an old record
// must never claim coverage of mutants it never generated
// (REQ-mut-operators, REQ-result-stale).
const OperatorSet = "go/2"

var comparisonSwap = map[token.Token]token.Token{
	token.EQL: token.NEQ, token.NEQ: token.EQL,
	token.LSS: token.GEQ, token.GEQ: token.LSS,
	token.GTR: token.LEQ, token.LEQ: token.GTR,
	token.LAND: token.LOR, token.LOR: token.LAND,
}

var arithmeticSwap = map[token.Token]token.Token{
	token.ADD: token.SUB, token.SUB: token.ADD,
	token.MUL: token.QUO, token.QUO: token.MUL,
}

var assignArithmeticSwap = map[token.Token]token.Token{
	token.ADD_ASSIGN: token.SUB_ASSIGN, token.SUB_ASSIGN: token.ADD_ASSIGN,
	token.MUL_ASSIGN: token.QUO_ASSIGN, token.QUO_ASSIGN: token.MUL_ASSIGN,
}

// Mutants generates up to budget mutants of the symbol's body (0 means all),
// in source order — deterministic (REQ-mut-operators, REQ-mut-budget).
// Mutants that render identically to the baseline are dropped here; ones
// that fail to compile are discarded by the runner.
func (t *Tree) Mutants(symbol string, budget int) ([]Mutant, error) {
	fd, pkg, err := t.funcDecl(symbol)
	if err != nil {
		return nil, err
	}
	if fd.Body == nil {
		return nil, nil
	}
	file, path, err := t.fileOf(pkg, fd.Pos())
	if err != nil {
		return nil, err
	}
	baseline, err := renderFile(pkg.Fset, file)
	if err != nil {
		return nil, err
	}

	type site struct {
		op     string
		pos    token.Pos
		apply  func()
		revert func()
	}
	var sites []site

	// numeric reports whether the expression's type is numeric, so
	// arithmetic swaps never touch string concatenation.
	numeric := func(e ast.Expr) bool {
		basic, ok := pkg.TypesInfo.TypeOf(e).(*types.Basic)
		return ok && basic.Info()&types.IsNumeric != 0
	}
	boolTrue, boolFalse := ast.NewIdent("true"), ast.NewIdent("false")

	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.BinaryExpr:
			if swapped, ok := comparisonSwap[v.Op]; ok {
				orig := v.Op
				sites = append(sites, site{
					op:    fmt.Sprintf("%s -> %s", orig, swapped),
					pos:   v.OpPos,
					apply: func() { v.Op = swapped }, revert: func() { v.Op = orig },
				})
			}
			if swapped, ok := arithmeticSwap[v.Op]; ok && numeric(v.X) {
				orig := v.Op
				sites = append(sites, site{
					op:    fmt.Sprintf("%s -> %s", orig, swapped),
					pos:   v.OpPos,
					apply: func() { v.Op = swapped }, revert: func() { v.Op = orig },
				})
			}
			// Forcing one operand of a logical pair to its identity makes the
			// other term decide alone; to its absorbing element, the whole
			// expression — both probe whether the term matters.
			if v.Op == token.LAND || v.Op == token.LOR {
				forced := boolTrue
				if v.Op == token.LOR {
					forced = boolFalse
				}
				for _, side := range []*ast.Expr{&v.X, &v.Y} {
					s, orig := side, *side
					sites = append(sites, site{
						op:    "force " + forced.Name,
						pos:   orig.Pos(),
						apply: func() { *s = forced }, revert: func() { *s = orig },
					})
				}
			}
		case *ast.BasicLit:
			if v.Kind == token.INT {
				orig := v.Value
				sites = append(sites, site{
					op:    "increment literal",
					pos:   v.Pos(),
					apply: func() { v.Value = incrementInt(orig) }, revert: func() { v.Value = orig },
				})
			}
		case *ast.BranchStmt:
			if v.Tok == token.BREAK || v.Tok == token.CONTINUE {
				orig, swapped := v.Tok, token.CONTINUE
				if orig == token.CONTINUE {
					swapped = token.BREAK
				}
				sites = append(sites, site{
					op:    fmt.Sprintf("%s -> %s", orig, swapped),
					pos:   v.Pos(),
					apply: func() { v.Tok = swapped }, revert: func() { v.Tok = orig },
				})
			}
		case *ast.IncDecStmt:
			orig, swapped := v.Tok, token.DEC
			if orig == token.DEC {
				swapped = token.INC
			}
			sites = append(sites, site{
				op:    fmt.Sprintf("%s -> %s", orig, swapped),
				pos:   v.TokPos,
				apply: func() { v.Tok = swapped }, revert: func() { v.Tok = orig },
			})
		case *ast.AssignStmt:
			if swapped, ok := assignArithmeticSwap[v.Tok]; ok && numeric(v.Lhs[0]) {
				orig := v.Tok
				sites = append(sites, site{
					op:    fmt.Sprintf("%s -> %s", orig, swapped),
					pos:   v.TokPos,
					apply: func() { v.Tok = swapped }, revert: func() { v.Tok = orig },
				})
			}
		case *ast.IfStmt:
			orig := v.Cond
			sites = append(sites, site{
				op:  "negate condition",
				pos: v.Cond.Pos(),
				apply: func() {
					v.Cond = &ast.UnaryExpr{Op: token.NOT, X: &ast.ParenExpr{X: orig}}
				},
				revert: func() { v.Cond = orig },
			})
		case *ast.BlockStmt:
			for i, st := range v.List {
				switch typed := st.(type) {
				case *ast.ExprStmt, *ast.IncDecStmt, *ast.GoStmt, *ast.DeferStmt, *ast.SendStmt:
					idx, stmt, list := i, st, v
					sites = append(sites, site{
						op:  "delete statement",
						pos: st.Pos(),
						apply: func() {
							list.List = append(append([]ast.Stmt{}, list.List[:idx]...), list.List[idx+1:]...)
						},
						revert: func() {
							withStmt := append(append([]ast.Stmt{}, list.List[:idx]...), stmt)
							list.List = append(withStmt, list.List[idx:]...)
						},
					})
				case *ast.AssignStmt:
					// An assignment cannot be deleted compilably in general,
					// but its store can be dropped: assign the right-hand
					// side to blanks, keeping evaluation and losing the
					// write — the removal-class mutant (leaks, skipped state
					// updates). Declarations stay: removing one breaks later
					// uses, proving nothing.
					if typed.Tok == token.DEFINE {
						break
					}
					blanks := make([]ast.Expr, len(typed.Lhs))
					for j := range blanks {
						blanks[j] = ast.NewIdent("_")
					}
					noop := &ast.AssignStmt{Lhs: blanks, Tok: token.ASSIGN, Rhs: typed.Rhs}
					idx, orig, list := i, st, v
					sites = append(sites, site{
						op:     "drop assignment",
						pos:    st.Pos(),
						apply:  func() { list.List[idx] = noop },
						revert: func() { list.List[idx] = orig },
					})
				}
			}
		case *ast.ReturnStmt:
			for i, res := range v.Results {
				zero := zeroExpr(pkg.TypesInfo.TypeOf(res))
				if zero == nil {
					continue
				}
				idx, orig, ret := i, res, v
				sites = append(sites, site{
					op:    "zero return",
					pos:   res.Pos(),
					apply: func() { ret.Results[idx] = zero }, revert: func() { ret.Results[idx] = orig },
				})
			}
		}
		return true
	})

	var out []Mutant
	seen := map[string]bool{}
	identities := map[string]int{}
	for _, s := range sites {
		if budget > 0 && len(out) >= budget {
			break
		}
		s.apply()
		mutated, err := renderFile(pkg.Fset, file)
		s.revert()
		if err != nil || bytes.Equal(mutated, baseline) {
			continue
		}
		// Two operators occasionally render the same source; running the
		// duplicate would double-count one effective mutant.
		if key := string(mutated); seen[key] {
			continue
		} else {
			seen[key] = true
		}
		// A mutation that orphans an import must not die as a build failure:
		// prune imports so the mutant gets its day in court. Process only
		// formats and prunes here — it must never gain an add-import mode,
		// which would resolve against the inherited (unpinned) workspace.
		if fixed, err := imports.Process("mutant.go", mutated, nil); err == nil {
			mutated = fixed
		}
		p := pkg.Fset.Position(s.pos)
		position := fmt.Sprintf("%s:%d:%d", filepath.Base(p.Filename), p.Line, p.Column)
		identity := position + "|" + s.op
		identities[identity]++
		if identities[identity] > 1 {
			position += fmt.Sprintf("#%d", identities[identity])
		}
		out = append(out, Mutant{
			Symbol:       symbol,
			Operator:     s.op,
			Position:     position,
			Replacements: []Replacement{{File: path, Source: mutated}},
		})
	}
	return out, nil
}

// incrementInt renders an integer literal one greater; non-decimal spellings
// pass through a decimal round-trip only when they parse.
func incrementInt(lit string) string {
	n, err := strconv.ParseUint(lit, 0, 63)
	if err != nil {
		return lit // renders identically and is dropped as a no-op site
	}
	return strconv.FormatUint(n+1, 10)
}

// zeroExpr builds a zero-value expression for simple types; nil when the
// type has no obviously-compilable zero literal.
func zeroExpr(t types.Type) ast.Expr {
	switch v := t.(type) {
	case *types.Basic:
		info := v.Info()
		switch {
		case info&types.IsBoolean != 0:
			return ast.NewIdent("false")
		case info&types.IsNumeric != 0:
			return &ast.BasicLit{Kind: token.INT, Value: "0"}
		case info&types.IsString != 0:
			return &ast.BasicLit{Kind: token.STRING, Value: `""`}
		}
	case *types.Pointer, *types.Slice, *types.Map, *types.Chan, *types.Signature, *types.Interface:
		return ast.NewIdent("nil")
	case *types.Named:
		if _, ok := v.Underlying().(*types.Interface); ok {
			return ast.NewIdent("nil")
		}
	}
	return nil
}

// fileOf finds the syntax file (and its absolute path) containing pos.
func (t *Tree) fileOf(pkg *packages.Package, pos token.Pos) (*ast.File, string, error) {
	for _, f := range pkg.Syntax {
		if f.FileStart <= pos && pos < f.FileEnd {
			return f, pkg.Fset.Position(f.Pos()).Filename, nil
		}
	}
	return nil, "", fmt.Errorf("no syntax file for position")
}

// renderFile formats a (possibly mutated) syntax file back to source bytes.
func renderFile(fset *token.FileSet, file *ast.File) ([]byte, error) {
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, file); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
