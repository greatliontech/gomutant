//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris && !windows

package cmd

import (
	"context"
	"fmt"
)

func readStdinContext(context.Context) ([]byte, error) {
	return nil, fmt.Errorf("cancellable stdin is unsupported on this host")
}
