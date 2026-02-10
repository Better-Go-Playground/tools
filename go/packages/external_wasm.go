package packages

import (
	"os"
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
