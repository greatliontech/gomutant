package contextio

import (
	"bytes"
	"context"
	"io"
	"os"
)

const chunkSize = 32 * 1024

// ReadFile reads path while polling ctx between bounded chunks.
func ReadFile(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := openRegularRead(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var out bytes.Buffer
	buffer := make([]byte, chunkSize)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, readErr := file.Read(buffer)
		out.Write(buffer[:n])
		if readErr == io.EOF {
			return out.Bytes(), ctx.Err()
		}
		if readErr != nil {
			return nil, readErr
		}
	}
}

// WriteFile writes path while polling ctx between bounded chunks.
func WriteFile(ctx context.Context, path string, data []byte, mode os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := openRegularWrite(path, mode)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			file.Close()
		}
	}()
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunk := min(len(data), chunkSize)
		n, err := file.Write(data[:chunk])
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	closed = true
	return file.Close()
}
