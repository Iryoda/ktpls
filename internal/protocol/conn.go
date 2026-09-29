package protocol

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime/debug"
	"sync"
)

// Handler handles one incoming request or notification. For requests, the
// returned result (or error) is sent back to the client; for notifications
// both are discarded (errors are logged).
type Handler func(ctx context.Context, msg *Message) (result any, err error)

// Conn is a JSON-RPC 2.0 connection over a byte stream.
//
// Incoming messages are handled sequentially, in arrival order. This keeps
// document synchronization trivially consistent: a request always observes
// every notification sent before it.
type Conn struct {
	r   *bufio.Reader
	w   io.Writer
	log *slog.Logger

	wmu sync.Mutex // serializes writes
}

// NewConn returns a connection reading from r and writing to w.
func NewConn(r io.Reader, w io.Writer, log *slog.Logger) *Conn {
	return &Conn{r: bufio.NewReader(r), w: w, log: log}
}

// Run reads and handles messages until the stream ends or ctx is done.
// It returns nil on a clean EOF.
func (c *Conn) Run(ctx context.Context, h Handler) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		body, err := ReadMessage(c.r)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		var msg Message
		if err := json.Unmarshal(body, &msg); err != nil {
			c.log.Error("malformed message", "err", err)
			c.reply(nil, nil, Errorf(CodeParseError, "%v", err))
			continue
		}
		if msg.Method == "" {
			// A response to a server->client request. We don't issue any yet.
			continue
		}
		result, err := c.handle(ctx, h, &msg)
		if msg.IsCall() {
			c.reply(msg.ID, result, err)
		} else if err != nil {
			c.log.Error("notification failed", "method", msg.Method, "err", err)
		}
	}
}

// handle calls h, converting panics into internal errors so that one bad
// request (e.g. an unexpected syntax tree shape) cannot kill the server.
func (c *Conn) handle(ctx context.Context, h Handler, msg *Message) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			c.log.Error("panic in handler", "method", msg.Method, "panic", r, "stack", string(debug.Stack()))
			result, err = nil, Errorf(CodeInternalError, "internal error handling %s: %v", msg.Method, r)
		}
	}()
	return h(ctx, msg)
}

func (c *Conn) reply(id json.RawMessage, result any, err error) {
	if id == nil {
		id = json.RawMessage("null")
	}
	var resp any
	if err != nil {
		var rerr *ResponseError
		if !errors.As(err, &rerr) {
			rerr = Errorf(CodeRequestFailed, "%v", err)
		}
		resp = struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Error   *ResponseError  `json:"error"`
		}{"2.0", id, rerr}
	} else {
		resp = struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Result  any             `json:"result"`
		}{"2.0", id, result}
	}
	if werr := c.write(resp); werr != nil {
		c.log.Error("writing response", "err", werr)
	}
}

// Notify sends a notification to the client.
func (c *Conn) Notify(method string, params any) error {
	return c.write(struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{"2.0", method, params})
}

func (c *Conn) write(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshaling message: %w", err)
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return WriteMessage(c.w, body)
}
