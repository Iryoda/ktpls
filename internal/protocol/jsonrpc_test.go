package protocol

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
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
