package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/contextio"
	"github.com/spf13/cobra"
)

type ephemeralOptions struct {
	dir, file, replacement, batch, testPkg, runPat string
	timeout                                        time.Duration
}

func newEphemeralCommand() *cobra.Command {
	o := ephemeralOptions{}
	cmd := &cobra.Command{Use: "ephemeral", Short: "Run one manual mutant", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return ephemeralCommand(cmd.Context(), o)
	}}
	f := cmd.Flags()
	f.StringVar(&o.dir, "dir", ".", "tree root (module or workspace)")
	f.StringVar(&o.file, "file", "", "tree-relative source file to replace")
	f.StringVar(&o.replacement, "replacement", "", "path to the whole replacement source")
	f.StringVar(&o.batch, "batch", "", "JSON edit-batch path, or - for stdin")
	f.StringVar(&o.testPkg, "test-pkg", "", "package whose named test decides the kill")
	f.StringVar(&o.runPat, "run", "", "-run pattern naming the deciding test")
	f.DurationVar(&o.timeout, "timeout", 60*time.Second, "the run's budget")
	return cmd
}

func ephemeralCommand(ctx context.Context, o ephemeralOptions) error {
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
	tree, err := gomutant.LoadContext(ctx, o.dir)
	if err != nil {
		return err
	}
	var res *gomutant.EphemeralResult
	if o.batch != "" {
		res, err = tree.EphemeralBatch(ctx, batchEdits, o.testPkg, o.runPat, o.timeout)
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
		res, err = tree.Ephemeral(ctx, o.file, mutant, o.testPkg, o.runPat, o.timeout)
		if err != nil {
			return err
		}
	}
	if res.Killed {
		fmt.Printf("killed    %s  by %s\n", strings.Join(res.Files, ", "), res.Killer)
	} else {
		fmt.Printf("SURVIVED  %s  — %s did not notice the mutation\n", strings.Join(res.Files, ", "), res.Run)
	}
	return nil
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
