package analyzer

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

// When run as a fake analyzer, the test binary answers the protocol:
// "check" reports an error at "boom" in the text; "fail" returns an error.
func TestMain(m *testing.M) {
	if os.Getenv("KTPLS_FAKE_ANALYZER") == "1" {
		fakeAnalyzer()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeAnalyzer() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	out := json.NewEncoder(os.Stdout)
	for sc.Scan() {
		var req struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		json.Unmarshal(sc.Bytes(), &req)
		switch req.Method {
		case "shutdown":
			return
		case "init", "rebuild":
			out.Encode(map[string]any{"id": req.ID, "result": map[string]any{"files": 3, "millis": 1}})
		case "check":
			var p struct{ Path, Text string }
			json.Unmarshal(req.Params, &p)
			var diags []map[string]any
			if i := strings.Index(p.Text, "boom"); i >= 0 {
				diags = append(diags, map[string]any{"severity": "error", "start": i, "end": i + 4, "message": "boom!", "factory": "BOOM"})
			}
			out.Encode(map[string]any{"id": req.ID, "result": map[string]any{"path": p.Path, "diagnostics": diags}})
		default:
			out.Encode(map[string]any{"id": req.ID, "error": "unknown method " + req.Method})
		}
	}
}

func startFake(t *testing.T) *Client {
	t.Helper()
	t.Setenv("KTPLS_FAKE_ANALYZER", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c, err := Start(exe, "unused.jar", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestClient(t *testing.T) {
	c := startFake(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if r, err := c.Init(ctx, "model.txt", "/jdk"); err != nil || r.Files != 3 {
		t.Fatalf("init: %+v, %v", r, err)
	}
	ds, err := c.Check(ctx, "/p/A.kt", "val x = boom")
	if err != nil || len(ds) != 1 || ds[0].Start != 8 || ds[0].End != 12 || ds[0].Factory != "BOOM" {
		t.Fatalf("check: %+v, %v", ds, err)
	}
	if ds, err := c.Check(ctx, "/p/A.kt", "val x = 1"); err != nil || len(ds) != 0 {
		t.Fatalf("clean check: %+v, %v", ds, err)
	}
	if err := c.Call(ctx, "fail", nil, nil); err == nil || !strings.Contains(err.Error(), "unknown method fail") {
		t.Fatalf("error response: %v", err)
	}

	// Concurrent calls are matched to their own responses.
	errs := make(chan error, 20)
	for i := range 20 {
		go func() {
			text := "ok"
			if i%2 == 0 {
				text = "boom"
			}
			ds, err := c.Check(ctx, "/p/A.kt", text)
			if err == nil && (len(ds) == 1) != (i%2 == 0) {
				err = io.ErrUnexpectedEOF
			}
			errs <- err
		}()
	}
	for range 20 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent call: %v", err)
		}
	}

	c.Close()
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the analyzer didn't exit")
	}
	if _, err := c.Check(ctx, "/p/A.kt", "x"); err == nil {
		t.Error("call after exit: expected error")
	}
}
