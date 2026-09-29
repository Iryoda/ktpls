package protocol

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestFramingRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	msgs := []string{`{"a":1}`, `{}`, `{"text":"héllo 😀"}`}
	for _, m := range msgs {
		if err := WriteMessage(&buf, []byte(m)); err != nil {
			t.Fatal(err)
		}
	}
	r := bufio.NewReader(&buf)
	for _, want := range msgs {
		got, err := ReadMessage(r)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	if _, err := ReadMessage(r); !errors.Is(err, io.EOF) {
		t.Errorf("after last message: got %v, want io.EOF", err)
	}
}

func TestReadMessageHeaders(t *testing.T) {
	in := "content-length: 2\r\nContent-Type: application/vscode-jsonrpc; charset=utf-8\r\n\r\n{}"
	got, err := ReadMessage(bufio.NewReader(strings.NewReader(in)))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{}" {
		t.Errorf("got %q", got)
	}

	for _, bad := range []string{
		"Content-Type: x\r\n\r\n{}",    // no length
		"Content-Length: abc\r\n\r\n",  // bad length
		"Content-Length: 10\r\n\r\n{}", // truncated body
		"garbage\r\n\r\n",
	} {
		if _, err := ReadMessage(bufio.NewReader(strings.NewReader(bad))); err == nil {
			t.Errorf("ReadMessage(%q): expected error", bad)
		}
	}
}

func TestRequestWaitsForResponse(t *testing.T) {
	clientToServer, serverIn := io.Pipe()
	serverOut, serverToClient := io.Pipe()
	c := NewConn(clientToServer, serverToClient, slog.New(slog.NewTextHandler(io.Discard, nil)))
	go c.Run(context.Background(), func(context.Context, *Message) (any, error) { return nil, nil })
	defer serverIn.Close()

	// The client answers the server's request.
	go func() {
		r := bufio.NewReader(serverOut)
		body, err := ReadMessage(r)
		if err != nil {
			return
		}
		var msg Message
		json.Unmarshal(body, &msg)
		resp, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]string{"echo": msg.Method}})
		WriteMessage(serverIn, resp)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := c.Request(ctx, "window/workDoneProgress/create", map[string]string{"token": "t"})
	if err != nil || string(res) != `{"echo":"window/workDoneProgress/create"}` {
		t.Errorf("got %s, %v", res, err)
	}
}
