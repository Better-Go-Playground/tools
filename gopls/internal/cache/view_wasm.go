// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/internal/gocommand"
)

func loadGoEnv(_ context.Context, dir string, configEnv []string, _ *gocommand.Runner, vars map[string]*string) error {
	applyEnv := func(kv string) {
		key, val, ok := strings.Cut(kv, "=")
		if !ok {
			return
		}
		if ptr, ok := vars[key]; ok {
			*ptr = val
		}
	}

	for _, kv := range os.Environ() {
		applyEnv(kv)
	}
	for _, kv := range configEnv {
		applyEnv(kv)
	}

	modPtr, needsGoMod := vars["GOMOD"]
	workPtr, needsGoWork := vars["GOWORK"]
	if !needsGoMod && !needsGoWork {
		return nil
	}

	// Mock "go list" behavior which autopopulates GOWORK and GOMOD.
	const maxDirDepth = 10
	startDir := dir
	if startDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		startDir = wd
	}

	if needsGoMod {
		modPath, err := findFileUp(startDir, "go.mod", maxDirDepth)
		if err != nil {
			return err
		}

		if modPath == "" {
			modPath = "/dev/null"
		}

		*modPtr = modPath
	}

	if needsGoWork {
		workPath, err := findFileUp(startDir, "go.work", maxDirDepth)
		if err != nil {
			return err
		}

		if workPath != "" {
			*workPtr = workPath
		}
	}

	return nil
}

func findFileUp(startDir, name string, maxDepth int) (string, error) {
	dir := filepath.Clean(startDir)
	for i := 0; i < maxDepth && dir != ""; i++ {
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err == nil {
			if !info.IsDir() {
				if abs, err := filepath.Abs(candidate); err == nil {
					return abs, nil
				}
				return candidate, nil
			}
		} else if !os.IsNotExist(err) {
			return "", err
		}
		next := filepath.Dir(dir)
		if next == dir {
			break
		}
		dir = next
	}
	return "", nil
}
