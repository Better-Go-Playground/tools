package packages

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

type jsonRPCRequest struct {
	ID     int    `json:"id,omitempty"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type jsonRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (err *jsonRPCError) Error() string {
	return fmt.Sprintf("%s (code: %d)", err.Message, err.Code)
}

type jsonRPCResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *jsonRPCError   `json:"error"`
}

// wasmDriverTransport implements a Go packages driver transport for WebAssembly.
//
// Transport communicates over a pipe with separate read and write files.
type wasmDriverTransport struct {
	reader io.ReadCloser
	writer io.WriteCloser

	lock             sync.Mutex
	pendingResponses []chan jsonRPCResponse
	freeReqIDs       []int
}

func newWasmDriverTransport(addr string) (*wasmDriverTransport, error) {
	parts := strings.Split(addr, ":")
	if len(parts) != 2 {
		// TODO: support socket
		return nil, fmt.Errorf(`value should be in format 'stdin:stdout', got %q`, addr)
	}

	inPath := parts[0]
	outPath := parts[1]

	stdin, err := os.OpenFile(inPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to open stdin %q: %w", inPath, err)
	}

	stdout, err := os.Open(outPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open stdout %q: %w", outPath, err)
	}

	pendingResponses := make([]chan jsonRPCResponse, 0, 10)
	freeReqIDs := make([]int, len(pendingResponses))
	for i := range pendingResponses {
		freeReqIDs[i] = i
	}

	return &wasmDriverTransport{
		reader:           stdout,
		writer:           stdin,
		pendingResponses: pendingResponses,
		freeReqIDs:       freeReqIDs,
	}, nil
}

func reqIDToIndex(id int) (int, error) {
	i := id - 1
	if i < 0 {
		return 0, errors.New("reqIDToIndex: negative value")
	}

	return i, nil
}

func indexToReqID(i int) int {
	return i + 1
}

func (t *wasmDriverTransport) listen() {
	reader := bufio.NewReader(t.reader)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			logErr("wasmDriverTransport: failed to read response: %s", err)
			if len(line) == 0 {
				return
			}
		}

		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			if errors.Is(err, io.EOF) {
				return
			}
			continue
		}

		var rsp jsonRPCResponse
		if unmarshalErr := json.Unmarshal(line, &rsp); unmarshalErr != nil {
			logErr("wasmDriverTransport: failed to unmarshal response: %s", unmarshalErr)
			if errors.Is(err, io.EOF) {
				return
			}
			continue
		}

		if handleErr := t.handleResponse(rsp); handleErr != nil {
			logErr("wasmDriverTransport: %s", handleErr)
		}

		if errors.Is(err, io.EOF) {
			return
		}
	}
}

func (t *wasmDriverTransport) handleResponse(rsp jsonRPCResponse) (err error) {
	if rsp.ID <= 0 {
		// Notification or invalid JSON request errors don't have IDs.
		if rsp.Error != nil {
			logErr("wasmDriverTransport: received error from package driver: %s", rsp.Error)
			return
		}

		logErr("wasmDriverTransport: received orphan response from package driver: %q", rsp.Result)
		return
	}

	i, err := reqIDToIndex(rsp.ID)
	if err != nil {
		return err
	}

	t.lock.Lock()
	if i >= len(t.pendingResponses) {
		t.lock.Unlock()
		return fmt.Errorf("response id out of bounds %d", rsp.ID)
	}
	ch := t.pendingResponses[i]
	t.lock.Unlock()

	if ch == nil {
		return fmt.Errorf("response channel missing (reqID: %d)", rsp.ID)
	}

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("response channel closed (reqID: %d)", rsp.ID)
		}
	}()
	ch <- rsp
	return nil
}

func (t *wasmDriverTransport) cancelRequest(id int) {
	err := json.NewEncoder(t.writer).Encode(jsonRPCRequest{
		Method: "$/cancelRequest",
		Params: id,
	})
	if err != nil {
		logErr("wasmDriverTransport: can't cancel request %q: %s", id, err)
	}
}

func (t *wasmDriverTransport) getReqID() (int, chan jsonRPCResponse) {
	t.lock.Lock()
	defer t.lock.Unlock()

	if len(t.freeReqIDs) != 0 {
		i := t.freeReqIDs[len(t.freeReqIDs)-1]
		t.freeReqIDs = t.freeReqIDs[:len(t.freeReqIDs)-1]

		ch := make(chan jsonRPCResponse)
		t.pendingResponses[i] = ch
		return indexToReqID(i), ch
	}

	ch := make(chan jsonRPCResponse)
	t.pendingResponses = append(t.pendingResponses, ch)
	i := len(t.pendingResponses) - 1
	return indexToReqID(i), ch
}

func (t *wasmDriverTransport) releaseReqID(id int) {
	t.lock.Lock()
	defer t.lock.Unlock()

	i, err := reqIDToIndex(id)
	if err != nil {
		logErr("wasmDriverTransport: cannot release invalid request id %d: %s", i, err)
	}

	if i >= len(t.pendingResponses) {
		logErr("wasmDriverTransport: cannot release invalid request id %d", i)
		return
	}

	close(t.pendingResponses[i])
	t.freeReqIDs = append(t.freeReqIDs, i)
}

func (t *wasmDriverTransport) doRequest(ctx context.Context, req jsonRPCRequest, out any) error {
	reqID, ch := t.getReqID()
	req.ID = reqID

	err := json.NewEncoder(t.writer).Encode(req)
	if err != nil {
		t.releaseReqID(reqID)
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	defer t.releaseReqID(reqID)
	select {
	case <-ctx.Done():
		t.cancelRequest(reqID)
		return ctx.Err()
	case rsp, ok := <-ch:
		if !ok {
			return fmt.Errorf("wasmDriverTransport: response channel closed (reqID: %d)", reqID)
		}

		if rsp.Error != nil {
			return rsp.Error
		}

		if err := json.Unmarshal(rsp.Result, out); err != nil {
			return fmt.Errorf("failed to unmarshal response body: %w", err)
		}

		return nil
	}
}

type driverRequestEnvelope struct {
	WorkDir       string        `json:"workDir"`
	Patterns      []string      `json:"patterns"`
	DriverRequest DriverRequest `json:"driverRequest"`
}

func (t *wasmDriverTransport) driverRequest(ctx context.Context, msg driverRequestEnvelope) (*DriverResponse, error) {
	// Request ID is populated by doRequest.
	rsp := new(DriverResponse)
	req := jsonRPCRequest{
		Method: "goPackageDriver/query",
		Params: msg,
	}

	err := t.doRequest(ctx, req, rsp)
	return rsp, err
}

func logErr(format string, args ...any) {
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}

	fmt.Fprintln(os.Stderr, msg)
}

const wasmDriverPfx = "wasm:"

func buildDriver(tool string) driver {
	// On WASM, driver should be in format "wasm:stdin:stdout"
	if !strings.HasPrefix(tool, wasmDriverPfx) {
		return nil
	}

	addr := tool[len(wasmDriverPfx):]
	transport, err := newWasmDriverTransport(addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, `GOPACKAGESDRIVER: %s\n`, err)
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
