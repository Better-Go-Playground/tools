package gocommand

// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

import (
	"context"
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

var goMinorVerRegex = regexp.MustCompile(`(?m)^go1\.(\d+)`)

func GoVersion(ctx context.Context, inv Invocation, r *Runner) (int, error) {
	// External toolchain not available, report runtime info.
	rv := runtime.Version()
	matches := goMinorVerRegex.FindStringSubmatch(rv)
	if matches == nil {
		return 0, fmt.Errorf("bad ReleaseTags output: %q", rv)
	}

	minorVer, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, fmt.Errorf("no parseable ReleaseTags in %v", rv)
	}

	return minorVer, nil
}

// GoVersionOutput returns the complete output of the go version command.
func GoVersionOutput(ctx context.Context, inv Invocation, r *Runner) (string, error) {
	// External toolchain not available, report runtime info.
	sb := strings.Builder{}
	sb.WriteString("go version ")
	sb.WriteString(runtime.Version())
	sb.WriteRune(' ')
	sb.WriteString(runtime.GOOS)
	sb.WriteRune('/')
	sb.WriteString(runtime.GOARCH)
	return sb.String(), nil
}
