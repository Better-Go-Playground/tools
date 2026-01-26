//go:build !wasm

// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/label"
	"golang.org/x/tools/internal/event"
)

// modWhyImpl returns the result of "go mod why -m" on the specified go.mod file.
func modWhyImpl(ctx context.Context, snapshot *Snapshot, fh file.Handle) (map[string]string, error) {
	ctx, done := event.Start(ctx, "cache.ModWhy", label.URI.Of(fh.URI()))
	defer done()

	pm, err := snapshot.ParseMod(ctx, fh)
	if err != nil {
		return nil, err
	}
	// No requires to explain.
	if len(pm.File.Require) == 0 {
		return nil, nil // empty result
	}
	// Run `go mod why` on all the dependencies.
	args := []string{"why", "-m"}
	for _, req := range pm.File.Require {
		args = append(args, req.Mod.Path)
	}
	inv, cleanupInvocation, err := snapshot.GoCommandInvocation(NoNetwork, fh.URI().DirPath(), "mod", args)
	if err != nil {
		return nil, err
	}
	defer cleanupInvocation()
	stdout, err := snapshot.View().GoCommandRunner().Run(ctx, *inv)
	if err != nil {
		return nil, err
	}
	whyList := strings.Split(stdout.String(), "\n\n")
	if len(whyList) != len(pm.File.Require) {
		return nil, fmt.Errorf("mismatched number of results: got %v, want %v", len(whyList), len(pm.File.Require))
	}
	why := make(map[string]string, len(pm.File.Require))
	for i, req := range pm.File.Require {
		why[req.Mod.Path] = whyList[i]
	}
	return why, nil
}
