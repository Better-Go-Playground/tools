//go:build !wasm

package packages

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
)

func buildDriver(tool string) driver {
	return func(cfg *Config, patterns []string) (*DriverResponse, error) {
		req, err := json.Marshal(DriverRequest{
			Mode:       cfg.Mode,
			Env:        cfg.Env,
			BuildFlags: cfg.BuildFlags,
			Tests:      cfg.Tests,
			Overlay:    cfg.Overlay,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to encode message to driver tool: %v", err)
		}

		buf := new(bytes.Buffer)
		stderr := new(bytes.Buffer)
		cmd := exec.CommandContext(cfg.Context, tool, patterns...)
		cmd.Dir = cfg.Dir
		// The cwd gets resolved to the real path. On Darwin, where
		// /tmp is a symlink, this breaks anything that expects the
		// working directory to keep the original path, including the
		// go command when dealing with modules.
		//
		// os.Getwd stdlib has a special feature where if the
		// cwd and the PWD are the same node then it trusts
		// the PWD, so by setting it in the env for the child
		// process we fix up all the paths returned by the go
		// command.
		//
		// (See similar trick in Invocation.run in ../../internal/gocommand/invoke.go)
		cmd.Env = append(slices.Clip(cfg.Env), "PWD="+cfg.Dir)
		cmd.Stdin = bytes.NewReader(req)
		cmd.Stdout = buf
		cmd.Stderr = stderr

		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("%v: %v: %s", tool, err, cmd.Stderr)
		}
		if len(stderr.Bytes()) != 0 && os.Getenv("GOPACKAGESPRINTDRIVERERRORS") != "" {
			fmt.Fprintf(os.Stderr, "%s stderr: <<%s>>\n", cmdDebugStr(cmd), stderr)
		}

		var response DriverResponse
		if err := json.Unmarshal(buf.Bytes(), &response); err != nil {
			return nil, err
		}
		return &response, nil
	}
}
