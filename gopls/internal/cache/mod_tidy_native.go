//go:build !wasm

// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"
	"os"
	"path/filepath"

	"golang.org/x/mod/modfile"

	"golang.org/x/tools/gopls/internal/label"
	"golang.org/x/tools/internal/event"
)

// modTidyImpl runs "go mod tidy" on a go.mod file.
func modTidyImpl(ctx context.Context, snapshot *Snapshot, pm *ParsedModule) (*TidiedModule, error) {
	ctx, done := event.Start(ctx, "cache.ModTidy", label.URI.Of(pm.URI))
	defer done()

	tempDir, cleanup, err := TempModDir(ctx, snapshot, pm.URI)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	args := []string{"tidy", "-modfile=" + filepath.Join(tempDir, "go.mod")}
	inv, cleanupInvocation, err := snapshot.GoCommandInvocation(NoNetwork, pm.URI.DirPath(), "mod", args, "GOWORK=off")
	if err != nil {
		return nil, err
	}
	defer cleanupInvocation()
	if _, err := snapshot.view.gocmdRunner.Run(ctx, *inv); err != nil {
		return nil, err
	}

	// Go directly to disk to get the temporary mod file,
	// since it is always on disk.
	tempMod := filepath.Join(tempDir, "go.mod")
	tempContents, err := os.ReadFile(tempMod)
	if err != nil {
		return nil, err
	}
	ideal, err := modfile.Parse(tempMod, tempContents, nil)
	if err != nil {
		// We do not need to worry about the temporary file's parse errors
		// since it has been "tidied".
		return nil, err
	}

	// Compare the original and tidied go.mod files to compute errors and
	// suggested fixes.
	diagnostics, err := modTidyDiagnostics(ctx, snapshot, pm, ideal)
	if err != nil {
		return nil, err
	}

	return &TidiedModule{
		Diagnostics:   diagnostics,
		TidiedContent: tempContents,
	}, nil
}
