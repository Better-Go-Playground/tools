package main

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"strconv"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/debug"
	"golang.org/x/tools/gopls/internal/filecache"
	"golang.org/x/tools/gopls/internal/lsprpc"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/fakenet"
	versionpkg "golang.org/x/tools/gopls/internal/version"
	"golang.org/x/tools/internal/jsonrpc2"
)

var version = "" // if set by the linker, overrides the gopls version

func main() {
	versionpkg.VersionOverride = version

	// Force early creation of the filecache and refuse to start
	// if there were unexpected errors such as ENOSPC. This
	// minimizes the window of exposure to deletion of the
	// executable, and ensures that all subsequent calls to
	// filecache.Get cannot fail for these two reasons;
	// see issue #67433.
	//
	// This leaves only one likely cause for later failures:
	// deletion of the cache while gopls is running. If the
	// problem continues, we could periodically stat the cache
	// directory (for example at the start of every RPC) and
	// either re-create it or just fail the RPC with an
	// informative error and terminate the process.
	if _, err := filecache.Get("nonesuch", [32]byte{}); err != nil && err != filecache.ErrNotFound {
		log.Fatalf("gopls cannot access its persistent index (disk full?): %v", err)
	}

	ctx := context.Background()
	stream := jsonrpc2.NewHeaderStream(fakenet.NewConn("stdio", os.Stdin, os.Stdout))
	if isTraceEnabled() {
		stream = protocol.LoggingStream(stream, os.Stderr)
		ctx = debug.WithInstance(ctx)
	}

	srv := lsprpc.NewStreamServer(cache.New(nil), true, nil)
	conn := jsonrpc2.NewConn(stream)

	err := srv.ServeStream(ctx, conn)
	if err != nil && !errors.Is(err, io.EOF) {
		log.Fatalf("ServeStream: %s", err)
	}
}

func isTraceEnabled() bool {
	val := os.Getenv("LSP_TRACE")
	if val == "" {
		return false
	}

	v, _ := strconv.ParseBool(val)
	return v
}
