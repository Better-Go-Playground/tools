package packages

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type rpcRequest struct {
	ID     int    `json:"id,omitempty"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (err *rpcError) Error() string {
	return fmt.Sprintf("%s (code: %d)", err.Message, err.Code)
}

type rpcResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// ioDriverTransport implements a Go packages driver transport over file system.
//
// Transport communicates over a pipe with separate read and write file descriptors.
//
// Purpose of the transport is to provide a way to connect to existing package driver, rather than starting a process every time.
// The main use-case of the transport is to support environments that can't spawn external processes (wasip1, js).
type ioDriverTransport struct {
	reader io.ReadCloser
	writer io.WriteCloser

	lock             sync.Mutex
	pendingResponses []chan rpcResponse
	freeReqIDs       []int
}

func newIODriverTransport(r io.ReadCloser, w io.WriteCloser) *ioDriverTransport {
	pendingResponses := make([]chan rpcResponse, 0, 10)
	freeReqIDs := make([]int, len(pendingResponses))
	for i := range pendingResponses {
		freeReqIDs[i] = i
	}

	return &ioDriverTransport{
		reader:           r,
		writer:           w,
		pendingResponses: pendingResponses,
		freeReqIDs:       freeReqIDs,
	}
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

func logErr(format string, args ...any) {
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}

	fmt.Fprintln(os.Stderr, msg)
}

func (t *ioDriverTransport) listen() {
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

		var rsp rpcResponse
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

func (t *ioDriverTransport) handleResponse(rsp rpcResponse) (err error) {
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

func (t *ioDriverTransport) cancelRequest(id int) {
	err := json.NewEncoder(t.writer).Encode(rpcRequest{
		Method: "$/cancelRequest",
		Params: id,
	})
	if err != nil {
		logErr("wasmDriverTransport: can't cancel request %q: %s", id, err)
	}
}

func (t *ioDriverTransport) getReqID() (int, chan rpcResponse) {
	t.lock.Lock()
	defer t.lock.Unlock()

	if len(t.freeReqIDs) != 0 {
		i := t.freeReqIDs[len(t.freeReqIDs)-1]
		t.freeReqIDs = t.freeReqIDs[:len(t.freeReqIDs)-1]

		ch := make(chan rpcResponse)
		t.pendingResponses[i] = ch
		return indexToReqID(i), ch
	}

	ch := make(chan rpcResponse)
	t.pendingResponses = append(t.pendingResponses, ch)
	i := len(t.pendingResponses) - 1
	return indexToReqID(i), ch
}

func (t *ioDriverTransport) releaseReqID(id int) {
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

func (t *ioDriverTransport) doRequest(ctx context.Context, req rpcRequest, out any) error {
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

func (t *ioDriverTransport) driverRequest(ctx context.Context, msg driverRequestEnvelope) (*DriverResponse, error) {
	// Request ID is populated by doRequest.
	rsp := new(DriverResponse)
	req := rpcRequest{
		Method: "goPackageDriver/query",
		Params: msg,
	}

	err := t.doRequest(ctx, req, rsp)
	return rsp, err
}

const (
	addrPfxSock = "unix:"
	addrPfxFd   = "fd:"
)

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

	if strings.HasPrefix(val, addrPfxSock) {
		sockPath := strings.TrimSpace(val[len(addrPfxSock):])
		if sockPath == "" {
			return nil, fmt.Errorf("empty unix socket path in %q", val)
		}

		conn, err := net.Dial("unix", sockPath)
		if err != nil {
			return nil, fmt.Errorf("cannot connect to unix socket %q: %w", sockPath, err)
		}

		return newIODriverTransport(conn, conn), nil
	}

	if strings.HasPrefix(val, addrPfxFd) {
		raw := val[len(addrPfxFd):]
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
