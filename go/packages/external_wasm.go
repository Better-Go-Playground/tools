package packages

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func buildDriver(tool string) driver {
	if tool != "wasm" {
		return nil
	}

	val, ok := os.LookupEnv("GOPACKAGESDRIVERADDR")
	if !ok || val == "" {
		logErr("GOPACKAGESDRIVERADDR: missing addr")
		return nil
	}

	transport, err := ioDriverTransportFromAddr(val)
	if err != nil {
		logErr("GOPACKAGESDRIVERADDR: %s", err)
		return nil
	}

	go transport.listen()

	return func(cfg *Config, patterns []string) (*DriverResponse, error) {
		msg := driverRequestEnvelope{
			WorkDir:  cfg.Dir,
			Patterns: patterns,
			DriverRequest: DriverRequest{
				Mode:       cfg.Mode,
				Env:        cfg.Env,
				BuildFlags: cfg.BuildFlags,
				Tests:      cfg.Tests,
				Overlay:    cfg.Overlay,
			},
		}
		return transport.driverRequest(cfg.Context, msg)
	}
}

// ioDriverTransportFromAddr parses packages driver IO transport from address string.
//
// Supported formats:
//
//	"fd:3,4"					// Use file descriptors
//	"file:///foo,file:///bar"	// Use file descriptors
//
// File path or FD are coming on order as "stdin" and "stdout".
func ioDriverTransportFromAddr(val string) (*ioDriverTransport, error) {
	if val == "" {
		return nil, errors.New("empty transport addr")
	}

	if strings.HasPrefix(val, "fd:") {
		raw := val[3:]
		parts := strings.SplitN(raw, ",", 3)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid fd transport addr %q", val)
		}

		stdin, err := parseFD(parts[0], "stdin", val)
		if err != nil {
			return nil, err
		}
		stdout, err := parseFD(parts[1], "stdout", val)
		if err != nil {
			_ = stdin.Close()
			return nil, err
		}

		return newIODriverTransport(stdout, stdin), nil
	}

	if !strings.HasPrefix(val, "file:") {
		return nil, fmt.Errorf("invalid transport addr %q", val)
	}

	parts := strings.SplitN(val, ",", 3)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid file transport addr %q", val)
	}

	stdin, err := fileFromURL(strings.TrimSpace(parts[0]), true)
	if err != nil {
		return nil, fmt.Errorf("invalid stdin file URL %q: %w", parts[0], err)
	}

	stdout, err := fileFromURL(strings.TrimSpace(parts[1]), false)
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("invalid stdout file URL %q: %w", parts[1], err)
	}

	return newIODriverTransport(stdout, stdin), nil
}

func parseFD(raw, name, addr string) (*os.File, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("missing %s fd in %q", name, addr)
	}
	fd, err := strconv.Atoi(raw)
	if err != nil || fd < 0 {
		return nil, fmt.Errorf("invalid %s fd %q in %q", name, raw, addr)
	}

	f := os.NewFile(uintptr(fd), name)
	if f == nil {
		return nil, fmt.Errorf("invalid %s fd %d", name, fd)
	}
	return f, nil
}

func fileFromURL(raw string, write bool) (*os.File, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "file" {
		return nil, fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if u.Path == "" {
		return nil, errors.New("empty path")
	}

	fp := filepath.FromSlash(u.Path)
	var f *os.File

	if write {
		f, err = os.OpenFile(fp, os.O_WRONLY, 0)
	} else {
		f, err = os.Open(fp)
	}

	if err != nil {
		return nil, fmt.Errorf("cannot open %q: %w", fp, err)
	}

	return f, nil
}
