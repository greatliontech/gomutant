package gomutant

import (
	"context"
	"io"
	"os"
	"path/filepath"
)

// recordFileMode is the mode a record file beside the document keeps
// across rewrites: the file's own, or the default for a file written
// for the first time.
func recordFileMode(path string) (os.FileMode, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return 0o644, nil
	}
	if err != nil {
		return 0, err
	}
	return info.Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky), nil
}

// writeRecordFile replaces one record file beside the findings document
// atomically — the findings document, the ephemeral attestations, the
// exemption record: the contents land in a temporary file in the same
// directory (chunked, so a cancellation lands between chunks), take the
// mode the file kept, and move into place by rename; a failure at any
// step leaves the standing file as it was and removes the temporary.
// The one writer, so every record file keeps its mode and its trailing
// newline alike.
func writeRecordFile(ctx context.Context, path string, contents []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".gomutant-record-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	for len(contents) > 0 {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		chunk := min(len(contents), 32*1024)
		n, err := tmp.Write(contents[:chunk])
		if err != nil {
			return fail(err)
		}
		if n == 0 {
			return fail(io.ErrShortWrite)
		}
		contents = contents[n:]
	}
	if err := tmp.Chmod(mode); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := ctx.Err(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}
