package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// serve runs a one-request agent on a Unix socket and returns it. The panel's
// socket driver was only ever exercised against the fake agent, so this is the
// smallest honest test of the protocol: real socket, real newline framing.
func serve(t *testing.T, answer func(request map[string]any) any) string {
	t.Helper()

	// Unix sockets need a short path; t.TempDir() on macOS is long enough to
	// exceed the limit.
	dir, err := os.MkdirTemp("", "mc")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	path := filepath.Join(dir, "agent.sock")

	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Skipf("this host cannot listen on a Unix socket: %v", err)
	}

	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			go func(conn net.Conn) {
				defer conn.Close()

				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					return
				}

				var request map[string]any
				if err := json.Unmarshal(line, &request); err != nil {
					return
				}

				encoded, _ := json.Marshal(answer(request))
				_, _ = conn.Write(append(encoded, '\n'))
			}(conn)
		}
	}()

	return path
}

func TestSocketAgentCall(t *testing.T) {
	var seen map[string]any

	path := serve(t, func(request map[string]any) any {
		seen = request

		return map[string]any{
			"id":     request["id"],
			"result": map[string]any{"username": "siti", "domain": "siti.test"},
		}
	})

	client := NewSocketAgent(path, 5*time.Second)

	result, err := client.Call(context.Background(), "web.apply_domain", map[string]any{"username": "siti"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	if client.Name() != "socket" {
		t.Errorf("Name() = %q, want socket", client.Name())
	}

	if seen["jsonrpc"] != "2.0" || seen["method"] != "web.apply_domain" {
		t.Errorf("request = %v, want JSON-RPC 2.0 web.apply_domain", seen)
	}

	params, _ := seen["params"].(map[string]any)
	if params["username"] != "siti" {
		t.Errorf("params = %v, want username siti", params)
	}

	var answer struct {
		Username string `json:"username"`
	}

	if err := json.Unmarshal(result, &answer); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}

	if answer.Username != "siti" {
		t.Errorf("username = %q, want siti", answer.Username)
	}
}

// A refusal is an *Error with the reason the agent attached, not a transport
// failure: the panel shows the reason instead of "agent unreachable".
func TestSocketAgentErrorKeepsReason(t *testing.T) {
	path := serve(t, func(request map[string]any) any {
		return map[string]any{
			"id": request["id"],
			"error": map[string]any{
				"code":    -32000,
				"message": "the database already exists",
				"data":    map[string]any{"reason": "exists"},
			},
		}
	})

	client := NewSocketAgent(path, 5*time.Second)

	_, err := client.Call(context.Background(), "db.create_database", map[string]any{})
	if err == nil {
		t.Fatal("Call accepted a refusal")
	}

	var agentErr *Error
	if !errors.As(err, &agentErr) {
		t.Fatalf("error is %T, want *agent.Error", err)
	}

	if agentErr.Reason() != "exists" {
		t.Errorf("reason = %q, want exists", agentErr.Reason())
	}
}

// The agent answers with a null id when it rejects a call before reading it.
// That error must reach the caller instead of being reported as a mismatch.
func TestSocketAgentNullIDErrorIsPassedThrough(t *testing.T) {
	path := serve(t, func(map[string]any) any {
		return map[string]any{
			"id": nil,
			"error": map[string]any{
				"code":    -32600,
				"message": "the caller is not allowed to use this socket",
			},
		}
	})

	client := NewSocketAgent(path, 5*time.Second)

	if _, err := client.Call(context.Background(), "system.info", nil); err == nil {
		t.Fatal("Call accepted an error with a null id")
	}
}

func TestSocketAgentMissingSocket(t *testing.T) {
	client := NewSocketAgent(filepath.Join(os.TempDir(), "muriscore-no-such.sock"), 2*time.Second)

	if _, err := client.Call(context.Background(), "system.info", nil); err == nil {
		t.Fatal("Call succeeded with no socket")
	}
}
