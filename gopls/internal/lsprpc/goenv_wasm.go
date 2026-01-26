// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsprpc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func getGoEnv(_ context.Context, env map[string]any) (map[string]string, error) {
	envmap := make(map[string]string)
	for _, kv := range os.Environ() {
		if key, val, ok := strings.Cut(kv, "="); ok {
			envmap[key] = val
		}
	}
	for k, v := range env {
		if s, ok := v.(string); ok {
			envmap[k] = s
		} else {
			envmap[k] = fmt.Sprint(v)
		}
	}

	const maxDirDepth = 10
	startDir, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	if envmap["GOMOD"] == "" {
		modPath, err := findFileUp(startDir, "go.mod", maxDirDepth)
		if err != nil {
			return nil, err
		}

		if modPath == "" {
			modPath = "/dev/null"
		}

		envmap["GOMOD"] = modPath
	}

	if envmap["GOWORK"] == "" {
		workPath, err := findFileUp(startDir, "go.work", maxDirDepth)
		if err != nil {
			return nil, err
		}
		if workPath != "" {
			envmap["GOWORK"] = workPath
		}
	}

	return envmap, nil
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
