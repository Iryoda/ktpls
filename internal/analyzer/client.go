// Package analyzer runs ktpls's analyzer: a sibling JVM process running
// the Kotlin compiler's front end (the Kotlin Analysis API) over the
// project, for compiler diagnostics without a build. ktpls talks to it in
// JSON lines over stdin/stdout.
package analyzer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"time"
)

// A Client is a running analyzer process.
type Client struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	log *slog.Logger

	wmu    sync.Mutex // serializes requests on stdin
	mu     sync.Mutex
	nextID int
	calls  map[int]chan response
	err    error         // set when the process ended
	done   chan struct{} // closed when the process ended
}

type request struct {
	ID     int    `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type response struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Start starts the analyzer: java [jvmArgs...] -jar jar.
func Start(java, jar string, jvmArgs []string, log *slog.Logger) (*Client, error) {
	args := append(append([]string{}, jvmArgs...), "-jar", jar)
	cmd := exec.Command(java, args...)
	ownProcessGroup(cmd)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting the analyzer: %w", err)
	}
	c := &Client{cmd: cmd, in: in, log: log, calls: map[int]chan response{}, done: make(chan struct{})}
	go c.readLoop(out)
	go func() {
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			log.Debug("analyzer", "stderr", sc.Text())
		}
		if err := sc.Err(); err != nil {
			log.Debug("analyzer: reading stderr", "err", err)
			io.Copy(io.Discard, stderr) // keep draining, or the JVM blocks writing
		}
	}()
	return c, nil
}

func (c *Client) readLoop(out io.Reader) {
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20) // diagnostics of a big file
	for sc.Scan() {
		var r response
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			c.log.Warn("analyzer: malformed response", "err", err)
			continue
		}
		c.mu.Lock()
		ch := c.calls[r.ID]
		delete(c.calls, r.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- r
		}
	}
	if err := sc.Err(); err != nil {
		// A response too long to read: the protocol is out of step, so
		// the process is stopped (and restarted by its watcher).
		c.log.Warn("analyzer: reading responses", "err", err)
		killGroup(c.cmd)
	}
	err := c.cmd.Wait()
	if err == nil {
		err = errors.New("the analyzer exited")
	}
	c.mu.Lock()
	c.err = fmt.Errorf("analyzer: %w", err)
	calls := c.calls
	c.calls = map[int]chan response{}
	c.mu.Unlock()
	for _, ch := range calls {
		ch <- response{Error: "the analyzer exited"}
	}
	close(c.done)
}

// Call sends a request and decodes its result into result (if non-nil).
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return err
	}
	c.nextID++
	id := c.nextID
	ch := make(chan response, 1)
	c.calls[id] = ch
	c.mu.Unlock()

	body, err := json.Marshal(request{ID: id, Method: method, Params: params})
	if err != nil {
		return err
	}
	c.wmu.Lock()
	_, err = c.in.Write(append(body, '\n'))
	c.wmu.Unlock()
	if err != nil {
		return fmt.Errorf("analyzer: %w", err)
	}
	select {
	case r := <-ch:
		if r.Error != "" {
			return fmt.Errorf("analyzer: %s: %s", method, r.Error)
		}
		if result != nil {
			return json.Unmarshal(r.Result, result)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.calls, id)
		c.mu.Unlock()
		return ctx.Err()
	}
}

// Done is closed when the process has exited.
func (c *Client) Done() <-chan struct{} { return c.done }

// Close asks the analyzer to exit, and kills it if it doesn't promptly.
func (c *Client) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c.Call(ctx, "shutdown", nil, nil) // the process exits without replying
	c.in.Close()
	select {
	case <-c.done:
	case <-time.After(3 * time.Second):
		killGroup(c.cmd)
		<-c.done
	}
}
