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
	timeout, oracleTimeout                         time.Duration
	oracleMemoryMiB                                int64
	runs                                           int
	tags                                           []string
	toolchain                                      string
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
	f.StringVar(&o.batch, "batch", "", "JSON edit-batch path, or - for stdin")
	f.StringVar(&o.testPkg, "test-pkg", "", "package whose named test decides the kill")
	f.StringVar(&o.runPat, "run", "", "-run pattern naming the deciding test")
	f.DurationVar(&o.timeout, "timeout", 0, "cancel command work before result completion after this duration; 0 = unlimited")
	f.DurationVar(&o.oracleTimeout, "oracle-timeout", 0, "maximum duration of the baseline and mutant oracle processes; 0 derives the budget from the measured baseline (an explicit value is the override); the advisory coverage probe shares the baseline measurement leash either way")
	f.Int64Var(&o.oracleMemoryMiB, "oracle-memory-mib", 0, "memory ceiling for the probe's oracle process tree in MiB: 0 derives RAM/2 floored at 1 GiB, -1 disables")
	f.IntVar(&o.runs, "runs", 1, "run the mutant this many times (1-10): killed means every run killed - consecutive kills split deterministic kills from a property generator's draw luck")
	f.StringVar(&o.attest, "attest", "", "record the surviving probe as a judged equivalence with this reasoning, in the committed record beside the findings document; refused when the probe killed, was mixed, or never exercised the edit")
	f.StringVar(&o.findingsFile, "findings", defaultFindings, "findings document whose sibling ephemeral-attestation record --attest writes")
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
	gomutant.SetOracleMemoryLimit(oracleMemoryBytes(o.oracleMemoryMiB), 1)
	tree, err := gomutant.LoadContextSelection(ctx, o.dir, selectionOf(o.tags, o.toolchain))
	if err != nil {
		return err
	}
	var res *gomutant.EphemeralResult
	if o.batch != "" {
		res, err = tree.EphemeralBatch(ctx, batchEdits, o.testPkg, o.runPat, o.oracleTimeout, o.runs)
		if err != nil {
			return err
		}
	} else {
		if err := ctx.Err(); err != nil {
			return err
		}
		mutant, err := readFileContext(ctx, o.replacement)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		res, err = tree.Ephemeral(ctx, o.file, mutant, o.testPkg, o.runPat, o.oracleTimeout, o.runs)
		if err != nil {
			return err
		}
	}
	renderEphemeralVerdict(os.Stdout, res)
	if o.attest != "" {
		att, err := gomutant.AttestEphemeralEquivalence(ctx, o.dir, res, o.attest)
		if err != nil {
			return err
		}
		path := gomutant.EphemeralAttestationsPathFor(findingsAt(o.dir, o.findingsFile))
		if err := gomutant.RecordEphemeralAttestation(ctx, path, att); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "equivalence recorded  %s  %s — %s\n", att.EditDigest[:min(12, len(att.EditDigest))], path, att.Reason)
	}
	return nil
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
