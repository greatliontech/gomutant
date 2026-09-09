package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/contextio"
	"github.com/spf13/cobra"
)

type ephemeralOptions struct {
	dir, file, replacement, batch, testPkg, runPat string
	attest, findingsFile                           string
	reattest                                       bool
	timeout, oracleTimeout, progressEvery          time.Duration
	oracleMemoryMiB                                int64
	runs                                           int
	tags                                           []string
	toolchain                                      string
	output                                         io.Writer
}

func newEphemeralCommand() *cobra.Command {
	o := ephemeralOptions{}
	cmd := &cobra.Command{Use: "ephemeral", Short: guidanceShort("ephemeral"), Long: guidanceHelp("ephemeral"), Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return ephemeralCommand(cmd.Context(), o)
	}}
	f := cmd.Flags()
	f.StringVar(&o.dir, "dir", ".", "tree root (module or workspace)")
	f.StringVar(&o.file, "file", "", "tree-relative source file to replace")
	selectionFlags(f, &o.tags, &o.toolchain)
	f.StringVar(&o.replacement, "replacement", "", "path to the whole replacement source")
	f.StringVar(&o.batch, "batch", "", "JSON edit-batch path, or - for stdin: {\"edits\":[{\"file\",\"old_string\",\"new_string\"},…]} or the bare array, every match resolving against the original file")
	f.StringVar(&o.testPkg, "test-pkg", "", "package whose named test decides the kill: an import path, or a package directory spelled like go test does (. or ./x) resolved against --dir")
	f.StringVar(&o.runPat, "run", "", "-run pattern naming the deciding test")
	f.DurationVar(&o.timeout, "timeout", 0, "cancel command work before result completion after this duration; 0 = unlimited")
	progressIntervalFlag(f, &o.progressEvery, "cadence of the progress line naming the phase in flight (loading, baseline, mutant run, coverage) and the elapsed time; 0 disables")
	f.DurationVar(&o.oracleTimeout, "oracle-timeout", 0, "maximum duration of the baseline and mutant oracle processes; 0 derives the budget from the measured baseline (an explicit value is the override); the advisory coverage probe shares the baseline measurement leash either way")
	f.Int64Var(&o.oracleMemoryMiB, "oracle-memory-mib", 0, "memory ceiling for the probe's oracle process tree in MiB: 0 derives RAM/2 floored at 1 GiB, -1 disables")
	f.IntVar(&o.runs, "runs", 1, "run the mutant this many times (1-10): killed means every run killed - consecutive kills split deterministic kills from a property generator's draw luck")
	f.StringVar(&o.attest, "attest", "", "record the surviving probe as a judged equivalence with this reasoning, in the committed record beside the findings document; refused when the probe killed, was mixed, or could not establish that it reached the edit (a never-reached plain survivor is refused by the probe itself)")
	f.StringVar(&o.findingsFile, "findings", defaultFindings, "findings document whose sibling ephemeral-attestation record --attest writes and a surviving probe is matched against")
	f.BoolVar(&o.reattest, "reattest", false, "with --attest: replace an existing attestation of the same mutant instead of refusing")
	return cmd
}

func ephemeralCommand(ctx context.Context, o ephemeralOptions) error {
	if o.timeout < 0 {
		return fmt.Errorf("timeout must not be negative")
	}
	if o.oracleTimeout < 0 {
		return fmt.Errorf("oracle timeout must not be negative")
	}
	if o.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.timeout)
		defer cancel()
	}
	if o.testPkg == "" || o.runPat == "" {
		return fmt.Errorf("ephemeral needs --test-pkg and --run")
	}
	// The runs count and the attestation's reasoning are the caller's
	// inputs: refused here, before the form checks, the batch document's
	// read, the load, and the probe (REQ-exec-preparation) — the one
	// order both faces keep.
	if err := gomutant.ValidateEphemeralRuns(o.runs); err != nil {
		return err
	}
	if o.attest != "" {
		if err := gomutant.ValidateAttestationReason(o.attest); err != nil {
			return err
		}
	}
	forms := 0
	if o.replacement != "" {
		forms++
	}
	if o.batch != "" {
		forms++
	}
	if forms != 1 {
		return fmt.Errorf("ephemeral needs exactly one of --replacement or --batch")
	}
	if o.replacement != "" && o.file == "" {
		return fmt.Errorf("--replacement needs --file")
	}
	if o.batch != "" && o.file != "" {
		return fmt.Errorf("--batch carries its own files; omit --file")
	}
	var batchEdits []gomutant.BatchEdit
	if o.batch != "" {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := readInputContext(ctx, o.batch)
		if err != nil {
			return err
		}
		batchEdits, err = gomutant.ParseEditBatch(data)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	// The reporter names every phase as it begins and keeps a cadenced
	// progress line through the long stretches (the load, the baseline
	// probe, each mutant run, the coverage probe) so an interrupted
	// probe names the phase it was in (REQ-exec-run-status).
	out := o.output
	if out == nil {
		out = os.Stdout
	}
	// The cadence goroutine and the verb share the writer: serialized,
	// as the run verb's is.
	out = &syncWriter{w: out}
	rep := newRunReporter(out, false, 0)
	defer rep.stop()
	// Primed before the cadence starts: a tick never precedes the
	// loading line with run-shaped tallies a probe does not have.
	rep.phase("loading")
	rep.startCadence(o.progressEvery)
	// An interruption names the stretch it cut short, after the cadence
	// has stopped, so nothing trails the verdict or the refusal.
	interrupted := func(err error) error {
		if ctx.Err() != nil {
			rep.stop()
			rep.interrupted(ctx.Err().Error())
		}
		return err
	}
	rep.preparation(gomutant.PreparationEvent{Stage: gomutant.PreparationLoading})
	tree, err := gomutant.LoadContextSelection(ctx, o.dir, selectionOf(o.tags, o.toolchain))
	if err != nil {
		return interrupted(err)
	}
	req := gomutant.EphemeralRequest{Findings: gomutant.FindingsPathAt(o.dir, o.findingsFile), RefuseAttested: o.attest != "" && !o.reattest, TestPkg: o.testPkg, Run: o.runPat, OracleTimeout: o.oracleTimeout, Runs: o.runs, OracleMemoryBytes: gomutant.OracleMemoryBytesFromMiB(o.oracleMemoryMiB), Progress: rep.preparation}
	if o.batch != "" {
		req.BatchEdits = batchEdits
	} else {
		if err := ctx.Err(); err != nil {
			return interrupted(err)
		}
		mutant, err := readFileContext(ctx, o.replacement)
		if err != nil {
			return interrupted(err)
		}
		if err := ctx.Err(); err != nil {
			return interrupted(err)
		}
		req.File, req.Mutant = o.file, mutant
	}
	res, err := tree.RunEphemeral(ctx, req)
	if err != nil {
		return interrupted(err)
	}
	rep.epilogue(func(w io.Writer) { renderEphemeralVerdict(w, res) })
	if o.attest != "" {
		att, err := gomutant.AttestEphemeralEquivalence(ctx, o.dir, res, o.attest)
		if err != nil {
			return err
		}
		path := gomutant.EphemeralAttestationsPathFor(gomutant.FindingsPathAt(o.dir, o.findingsFile))
		if err := gomutant.RecordEphemeralAttestation(ctx, path, att, o.reattest); err != nil {
			return err
		}
		line := fmt.Sprintf("equivalence recorded  %s  %s — %s", att.EditDigest[:min(12, len(att.EditDigest))], path, att.Reason)
		if res.Attested != nil {
			// The verdict above showed the standing row; this write
			// superseded it, and the face says so beside the new one.
			line += fmt.Sprintf(" (supersedes %s: %s)", attestedDigest(res.Attested), res.Attested.Reason)
		}
		fmt.Fprintln(out, line)
	}
	return nil
}

// attestedDigest spells a row's digest the way every line naming the
// row spells it: the digest's head, and the digest form beside it
// where the row keys on another form than the canonical one, so the
// digest shown can be found in the record.
func attestedDigest(att *gomutant.EphemeralAttestation) string {
	head := att.EditDigest[:min(12, len(att.EditDigest))]
	if att.DigestForm != "" {
		head += " [" + att.DigestForm + "]"
	}
	return head
}

// renderEphemeralVerdict prints the probe's verdict face. A non-kill
// verdict names every unexercised replacement file: "did not notice"
// over a file no baseline-covered block touches would affirmatively
// assert the false reading the label exists to prevent
// (REQ-exec-ephemeral).
func renderEphemeralVerdict(w io.Writer, res *gomutant.EphemeralResult) {
	switch {
	case res.Killed:
		line := fmt.Sprintf("killed    %s  by %s", strings.Join(res.Files, ", "), res.Killer)
		if res.Runs > 1 {
			line += fmt.Sprintf("  (%d consecutive runs)", res.Runs)
		}
		fmt.Fprintln(w, line)
	case res.KilledRuns > 0:
		// A partial kill is a property generator's draw luck, never a
		// deterministic kill and never plain survival.
		fmt.Fprintf(w, "FLAKY     %s  — killed %d/%d runs by %s\n", strings.Join(res.Files, ", "), res.KilledRuns, res.Runs, res.Killer)
	case res.Attested != nil:
		// An attested survivor reads as one: the judged equivalence,
		// its digest and provenance, never a bare survival.
		att := res.Attested
		provenance := "dirty tree"
		if att.Commit != "" {
			provenance = att.Commit[:min(12, len(att.Commit))]
			if att.Dirty {
				provenance += ", dirty"
			}
		}
		fmt.Fprintf(w, "SURVIVED  %s  — attested %s at %s under %s %s: %s\n", strings.Join(res.Files, ", "), attestedDigest(att), provenance, att.TestPkg, att.Run, att.Reason)
	default:
		fmt.Fprintf(w, "SURVIVED  %s  — %s did not notice the mutation\n", strings.Join(res.Files, ", "), res.Run)
	}
	// The effective bound is part of the verdict's meaning — a timeout
	// kill under a 60s budget and one under 40m are different claims —
	// and in derived-budget mode this line is the only place the caller
	// learns what budget the derivation produced.
	if res.OracleBudget != "" {
		fmt.Fprintf(w, "oracle budget %s  (baseline measured %s)\n", res.OracleBudget, res.MeasuredBaseline)
	}
	// The ceiling is the same class of fact: a memory-shaped verdict
	// means what it means under the ceiling the probe ran under, and
	// this line is where the caller learns the derived one.
	if res.OracleMemoryBytes > 0 {
		fmt.Fprintf(w, "oracle memory %d MiB\n", res.OracleMemoryBytes>>20)
	} else {
		fmt.Fprintln(w, "oracle memory unlimited")
	}
	for _, f := range res.MutatedTests {
		fmt.Fprintf(w, "mutated test  %s  — the oracle's own source changed: the verdict is about the test (was the edited part load-bearing for %s?), never about the code under test\n", f, res.Run)
	}
	if len(res.PrunedImports) > 0 {
		fmt.Fprintf(w, "imports pruned  %s  — unreferenced in the mutant, dropped before compiling\n", strings.Join(res.PrunedImports, ", "))
	}
	if res.CoverageUnknown && !res.Killed {
		fmt.Fprintf(w, "coverage unknown  %s  — whether the probed run reached this replacement could not be established (the coverage probe failed or could not attribute it); this survival is unverified\n", strings.Join(res.CoverageUnknownFiles, ", "))
	}
	for _, f := range res.UnexercisedFiles {
		fmt.Fprintf(w, "unexercised  %s  — no baseline-covered block reaches this replacement (linked into the oracle's binary, never reached by the probed run); its survival is not evidence the oracle noticed anything\n", f)
	}
	if res.KillerOutput != "" {
		for _, l := range strings.Split(res.KillerOutput, "\n") {
			fmt.Fprintln(w, "  "+l)
		}
	}
}

func readInput(path string) ([]byte, error) {
	return readInputContext(context.Background(), path)
}

func readInputContext(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if path == "-" {
		return readStdinContext(ctx)
	}
	return readFileContext(ctx, path)
}

func readFileContext(ctx context.Context, path string) ([]byte, error) {
	return contextio.ReadFile(ctx, path)
}
