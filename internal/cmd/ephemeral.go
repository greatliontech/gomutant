package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/spf13/cobra"
)

type ephemeralOptions struct {
	dir, file, replacement, batch, testPkg, runPat string
	timeout                                        time.Duration
}

func newEphemeralCommand() *cobra.Command {
	o := ephemeralOptions{}
	cmd := &cobra.Command{Use: "ephemeral", Short: "Run one manual mutant", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		return ephemeralCommand(o)
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

func ephemeralCommand(o ephemeralOptions) error {
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
		data, err := readInput(o.batch)
		if err != nil {
			return err
		}
		batchEdits, err = gomutant.ParseEditBatch(data)
		if err != nil {
			return err
		}
	}
	tree, err := gomutant.Load(o.dir)
	if err != nil {
		return err
	}
	var res *gomutant.EphemeralResult
	if o.batch != "" {
		res, err = tree.EphemeralBatch(context.Background(), batchEdits, o.testPkg, o.runPat, o.timeout)
		if err != nil {
			return err
		}
	} else {
		mutant, err := os.ReadFile(o.replacement)
		if err != nil {
			return err
		}
		res, err = tree.Ephemeral(context.Background(), o.file, mutant, o.testPkg, o.runPat, o.timeout)
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
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}
