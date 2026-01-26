// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"
	"fmt"
	"os"

	"golang.org/x/tools/gopls/internal/label"
	"golang.org/x/tools/internal/event"
)

func modTidyImpl(ctx context.Context, _ *Snapshot, pm *ParsedModule) (*TidiedModule, error) {
	ctx, done := event.Start(ctx, "cache.ModTidy", label.URI.Of(pm.URI))
	defer done()

	// TODO: implement tidy. Right now return as-is
	fmt.Fprintln(os.Stderr, "TODO: cache.modTidyImpl")
	formatted, err := pm.File.Format()
	if err != nil {
		return nil, err
	}

	return &TidiedModule{
		Diagnostics:   nil,
		TidiedContent: formatted,
	}, nil
}
