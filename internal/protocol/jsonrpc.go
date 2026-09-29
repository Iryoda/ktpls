// Package protocol implements the subset of the Language Server Protocol
// used by ktpls: JSON-RPC 2.0 framing over a byte stream, the LSP
// message types, request dispatch, and position mapping.
package protocol

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Message is a JSON-RPC 2.0 message: a request (Method and ID set), a
// notification (Method set, no ID), or a response (ID set, no Method).
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
}

// IsCall reports whether m is a request that expects a response.
func (m *Message) IsCall() bool { return m.Method != "" && len(m.ID) > 0 && string(m.ID) != "null" }

// IsNotification reports whether m is a notification.
func (m *Message) IsNotification() bool { return m.Method != "" && !m.IsCall() }

// JSON-RPC and LSP error codes.
const (
	CodeParseError           = -32700
	CodeInvalidRequest       = -32600
	CodeMethodNotFound       = -32601
	CodeInvalidParams        = -32602
	CodeInternalError        = -32603
	CodeServerNotInitialized = -32002
	CodeRequestFailed        = -32803
)

// ResponseError is a JSON-RPC error object.
type ResponseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *ResponseError) Error() string { return fmt.Sprintf("jsonrpc error %d: %s", e.Code, e.Message) }

// Errorf returns a *ResponseError with the given code.
func Errorf(code int, format string, args ...any) *ResponseError {
	return &ResponseError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// ReadMessage reads one Content-Length framed message body from r.
func ReadMessage(r *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if err == io.EOF && line == "" && length == -1 {
				return nil, io.EOF
			}
			return nil, fmt.Errorf("reading header: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // end of headers
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("malformed header %q", line)
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 0 {
				return nil, fmt.Errorf("invalid Content-Length %q", value)
			}
			length = n
		}
		// Other headers (Content-Type) are ignored.
	}
	if length < 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, fmt.Errorf("reading body: %w", err)
	}
	return body, nil
}

// WriteMessage writes body to w with a Content-Length header.
func WriteMessage(w io.Writer, body []byte) error {
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err := w.Write(body)
	return err
}
