//go:build pipetransport

package packages

import (
	"log"
	"os"
)

func buildDriver(tool string) driver {
	log.Println("trying pipetransport backend")
	if tool != "pipetransport" {
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

		t := newRPCTraceCall(cfg, patterns)
		defer t.send()

		rsp, err := transport.driverRequest(cfg.Context, msg)
		t.setResult(rsp, err)
		return rsp, err
	}
}
