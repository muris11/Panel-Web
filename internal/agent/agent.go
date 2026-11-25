// Package agent talks to rayxod, the root daemon that is the only process
// allowed to change the server.
//
// The panel never runs a shell: it hands the agent one of the methods the
// agent has registered, and the agent validates every parameter again on its
// side. This package is the Go equivalent of the Laravel panel's
// SocketAgentClient, and speaks the same JSON-RPC 2.0 protocol over the same
// Unix socket.
package agent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"
)

// Agent is the one dependency the panel has on the server. The fake
// implementation exists so the panel can be developed and tested off-server.
type Agent interface {
	// Call runs one method and returns its raw JSON result.
	Call(ctx context.Context, method string, params any) (json.RawMessage, error)
	// Name says which driver answered, for the dashboard.
	Name() string
}

// Error is a failure the agent reported. It is not a transport error: the
// call reached the agent and the agent refused it.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (e *Error) Error() string { return e.Message }

// Reason returns the machine-readable reason the agent attached, e.g.
// "exists", when it sent one.
func (e *Error) Reason() string {
	var data struct {
		Reason string `json:"reason"`
	}

	if len(e.Data) == 0 || json.Unmarshal(e.Data, &data) != nil {
		return ""
	}

	return data.Reason
}

// SocketAgent calls a real rayxod.
type SocketAgent struct {
	socketPath string
	timeout    time.Duration
}

// NewSocketAgent returns an agent that dials socketPath, giving up after
// timeout unless the caller's context is shorter.
func NewSocketAgent(socketPath string, timeout time.Duration) *SocketAgent {
	return &SocketAgent{socketPath: socketPath, timeout: timeout}
}

func (a *SocketAgent) Name() string { return "socket" }

// Call sends one request and waits for its answer.
func (a *SocketAgent) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	var dialer net.Dialer

	conn, err := dialer.DialContext(ctx, "unix", a.socketPath)
	if err != nil {
		return nil, fmt.Errorf("the agent is not reachable at %s: %w", a.socketPath, err)
	}

	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	request, err := newRequest(method, params)
	if err != nil {
		return nil, err
	}

	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode the request: %w", err)
	}

	if _, err := conn.Write(append(encoded, '\n')); err != nil {
		return nil, fmt.Errorf("send the request: %w", err)
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read the answer: %w", err)
	}

	return decodeResponse(line, request.ID)
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func newRequest(method string, params any) (request, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return request{}, fmt.Errorf("generate a request id: %w", err)
	}

	if params == nil {
		params = struct{}{}
	}

	encoded, err := json.Marshal(params)
	if err != nil {
		return request{}, fmt.Errorf("encode the parameters: %w", err)
	}

	return request{
		JSONRPC: "2.0",
		ID:      json.RawMessage(strconv.Quote(hex.EncodeToString(raw))),
		Method:  method,
		Params:  encoded,
	}, nil
}

func decodeResponse(line []byte, id json.RawMessage) (json.RawMessage, error) {
	var response struct {
		ID     json.RawMessage `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *Error          `json:"error"`
	}

	if err := json.Unmarshal(line, &response); err != nil {
		return nil, fmt.Errorf("the agent answered with something that is not JSON: %w", err)
	}

	sameRequest := bytes.Equal(response.ID, id)

	// The agent answers with a null id when it rejects a call before reading
	// it, for example when the caller is not the panel's user. Pass that
	// error through as well instead of losing it.
	if response.Error != nil && (sameRequest || isNull(response.ID)) {
		return nil, response.Error
	}

	if !sameRequest {
		return nil, errors.New("the agent's answer does not match the request")
	}

	if isNull(response.Result) {
		return json.RawMessage(`{}`), nil
	}

	return response.Result, nil
}

func isNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)

	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}
