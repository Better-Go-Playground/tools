// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"
	"errors"
	"fmt"
	"os"

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

	// TODO: implement replacement
	fmt.Fprintln(os.Stderr, "TODO: cache.modWhyImpl")
	return nil, errors.New("modWhyImpl: not implemented")
}
