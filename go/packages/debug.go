package packages

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"
	"unsafe"
)

var (
	traceFile *os.File
	nextSpan  atomic.Uint64
)

const skipFrameCount = 2

type stackFrame struct {
	Func string `json:"func"`
	At   string `json:"at"`
}

func getStackTrace(skip int) []stackFrame {
	pc := make([]uintptr, 64)

	// skip+1 to account for runtime.Callers itself
	n := runtime.Callers(skip+1, pc)
	frames := runtime.CallersFrames(pc[:n])

	out := make([]stackFrame, 0, cap(pc))
	for {
		frame, more := frames.Next()
		out = append(out, stackFrame{
			Func: frame.Function,
			At:   fmt.Sprintf("%s:%d", frame.File, frame.Line),
		})

		if !more {
			break
		}
	}

	return out
}

func TraceBegin() {
	e, ok := os.LookupEnv("LSP_PKG_TRACE")
	if !ok || e == "" {
		return
	}

	fp, err := filepath.Abs(e)
	if err != nil {
		logErr("TraceBegin: Abs failed %q", e)
		fp = e
	}

	f, err := os.OpenFile(fp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		logErr("TraceBegin: can't open trace file %q: %s", fp, err)
	}

	traceFile = f
}

func TraceEnd() {
	if traceFile == nil {
		return
	}

	traceFile.Close()
	traceFile.Sync()
}

type result[T any] struct {
	Ok    T      `json:"ok,omitempty"`
	Error string `json:"error,omitempty"`
}

type spanInfo struct {
	SpanID       uint64 `json:"spanId,omitempty"`
	ParentSpanID uint64 `json:"parentSpanId,omitempty"`
}

type traceHeader struct {
	spanInfo
	TS    int64        `json:"ts"`
	Stack []stackFrame `json:"stack"`
}

type traceCmd struct {
	header traceHeader
	Verb   string      `json:"verb"`
	Args   []string    `json:"args"`
	Result result[any] `json:"result"`
}

func newTraceCmd(ctx context.Context, verb string, args []string) (*traceCmd, context.Context) {
	ts := time.Now().UnixMilli()
	stack := getStackTrace(skipFrameCount)
	span, sctx := spanFromContext(ctx)
	return &traceCmd{
		header: traceHeader{
			spanInfo: span,
			TS:       ts,
			Stack:    stack,
		},
		Verb: verb,
		Args: args,
	}, sctx
}

func (t *traceCmd) send() {
	if traceFile == nil {
		return
	}

	traceSend(traceMsg{
		traceHeader: t.header,
		Cmd:         t,
	})
}

type overlay struct {
	Path    string `json:"path,omitempty"`
	Content struct {
		Replace map[string]string `json:"replace,omitempty"`
	}
}

type traceDrv struct {
	header   traceHeader
	Overlay  *overlay                `json:"overlay,omitempty"`
	Cwd      string                  `json:"cwd,omitempty"`
	Patterns []string                `json:"patterns"`
	Req      DriverRequest           `json:"req"`
	Result   result[*DriverResponse] `json:"result"`
}

func tryReadOverlay(ovFile string) *overlay {
	if ovFile == "" {
		return nil
	}

	ov := &overlay{
		Path: ovFile,
	}

	f, err := os.Open(ovFile)
	if err != nil {
		logErr("tryReadOverlay: can't open overlay: %s", err)
		return ov
	}

	defer f.Close()
	if err := json.NewDecoder(f).Decode(&ov.Content); err != nil {
		logErr("tryReadOverlay: can't parse overlay file %q: %s", ovFile, err)
	}

	return ov
}

func newTraceDrv(cfg *Config, overlayFile string, patterns []string) (*traceDrv, context.Context) {
	ts := time.Now().UnixMilli()
	stack := getStackTrace(skipFrameCount)
	span, ctx := spanFromContext(cfg.Context)

	drv := &traceDrv{
		header: traceHeader{
			spanInfo: span,
			TS:       ts,
			Stack:    stack,
		},
		Cwd:      cfg.Dir,
		Patterns: patterns,
		Overlay:  tryReadOverlay(overlayFile),
		Req: DriverRequest{
			Mode:       cfg.Mode,
			Env:        cfg.Env,
			BuildFlags: cfg.BuildFlags,
			Tests:      cfg.Tests,
			Overlay:    cfg.Overlay,
		},
	}

	return drv, ctx
}

func (t *traceDrv) send() {
	if traceFile == nil {
		return
	}

	traceSend(traceMsg{
		traceHeader: t.header,
		Drv:         t,
	})
}

type ctxKeyType string

const spanCtxKey = ctxKeyType("$ctxSpan")

func spanFromContext(parentCtx context.Context) (spanInfo, context.Context) {
	parent, _ := getTraceID(parentCtx)

	tid, ctx := newTraceID(parentCtx)
	return spanInfo{
		SpanID:       tid,
		ParentSpanID: parent,
	}, ctx
}

func newTraceID(ctx context.Context) (uint64, context.Context) {
	spanID := nextSpan.Add(1)
	if ctx == nil {
		ctx = context.Background()
	}

	sCtx := context.WithValue(ctx, spanCtxKey, spanID)
	return spanID, sCtx
}

func getTraceID(ctx context.Context) (uint64, bool) {
	if ctx == nil {
		return 0, false
	}

	r := ctx.Value(spanCtxKey)
	if r == nil {
		return 0, false
	}

	id, ok := r.(uint64)
	return id, ok
}

type traceMsg struct {
	Cmd *traceCmd `json:"cmd,omitempty"`
	Drv *traceDrv `json:"drv,omitempty"`

	traceHeader
}

func (cmd *traceCmd) setOutput(buff *bytes.Buffer) {
	b := buff.Bytes()
	if json.Valid(b) {
		data := make(map[string]any)
		if err := json.Unmarshal(b, &data); err == nil {
			cmd.Result.Ok = data
			return
		}
	}

	ptr := unsafe.SliceData(b)
	cmd.Result.Ok = unsafe.String(ptr, len(b))
}

func traceSend(msg traceMsg) {
	if traceFile == nil {
		return
	}

	err := json.NewEncoder(traceFile).Encode(msg)
	if err != nil {
		logErr("traceSend: %s", err)
	}
}
