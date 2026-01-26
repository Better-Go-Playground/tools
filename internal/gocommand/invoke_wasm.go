// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gocommand is a helper for calling the go command.
package gocommand

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// runCmdContext is like exec.CommandContext except it sends os.Interrupt
// before os.Kill.
func runCmdContext(ctx context.Context, cmd *exec.Cmd) (err error) {
	fmt.Fprintf(os.Stderr, "TODO: runCmdContext %q\n", cmd.Args)
	return syscall.ENOTSUP
}
