package control

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

type Client struct {
	socketPath string
}

func NewClient(socketPath string) *Client {
	if socketPath == "" {
		socketPath = DefaultSocketPath()
	}
	return &Client{socketPath: socketPath}
}

func roundTrip[T any](socketPath string, req Request) (*T, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("connecting to daemon: %w (is the daemon running?)", err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return nil, fmt.Errorf("writing request: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
	if !scanner.Scan() {
		return nil, fmt.Errorf("reading response: connection closed")
	}

	var resp T
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	return &resp, nil
}

// Activate sends an activate request to the daemon. If session > 0, the
// daemon tracks that PID as a reference holder for the activation.
func (c *Client) Activate(
	dir string,
	env map[string]string,
	session int,
) (*ActivateResponse, error) {
	return roundTrip[ActivateResponse](c.socketPath, Request{
		Version: ProtocolVersion,
		Type:    "activate",
		Dir:     dir,
		Env:     env,
		Session: session,
	})
}

// Deactivate sends a deactivate request to the daemon. If session > 0, only
// that session reference is removed; the activation persists until all sessions
// have left. If session == 0, the activation is force-removed.
func (c *Client) Deactivate(dir string, session int) (*DeactivateResponse, error) {
	return roundTrip[DeactivateResponse](c.socketPath, Request{
		Version: ProtocolVersion,
		Type:    "deactivate",
		Dir:     dir,
		Session: session,
	})
}

func (c *Client) Status() (*StatusResponse, error) {
	return roundTrip[StatusResponse](c.socketPath, Request{
		Version: ProtocolVersion,
		Type:    "status",
	})
}

func (c *Client) CacheStats() (*CacheStatsResponse, error) {
	return roundTrip[CacheStatsResponse](c.socketPath, Request{
		Version: ProtocolVersion,
		Type:    "cache_stats",
	})
}

func (c *Client) CacheGet(key string) (*CacheGetResponse, error) {
	return roundTrip[CacheGetResponse](c.socketPath, Request{
		Version: ProtocolVersion,
		Type:    "cache_get",
		Key:     key,
	})
}

func (c *Client) CacheClear() (*CacheClearResponse, error) {
	return roundTrip[CacheClearResponse](c.socketPath, Request{
		Version: ProtocolVersion,
		Type:    "cache_clear",
	})
}
