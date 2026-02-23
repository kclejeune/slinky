// Package control implements a Unix socket server and client for the
// slinky daemon control protocol. Shell hooks use the client to send
// activate commands that switch the active secret context.
package control

type ActivateResponse struct {
	OK       bool     `json:"ok"`
	Files    []string `json:"files,omitempty"`
	Error    string   `json:"error,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

type DeactivateResponse struct {
	OK    bool     `json:"ok"`
	Files []string `json:"files,omitempty"`
	Error string   `json:"error,omitempty"`
}

type StatusResponse struct {
	Running    bool                `json:"running"`
	ConfigHash string              `json:"config_hash,omitempty"`
	ActiveDirs []string            `json:"active_dirs,omitempty"`
	Files      []string            `json:"files,omitempty"`
	Layers     map[string][]string `json:"layers,omitempty"`
	Sessions   map[string][]int    `json:"sessions,omitempty"`
}

const ProtocolVersion = 1

type Request struct {
	Version int               `json:"version,omitempty"`
	Type    string            `json:"type"`
	Dir     string            `json:"dir,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Session int               `json:"session,omitempty"`
	Key     string            `json:"key,omitempty"`
}

type CacheStatsResponse struct {
	OK      bool                      `json:"ok"`
	Entries map[string]CacheEntryInfo `json:"entries"`
	Cipher  string                    `json:"cipher"`
}

type CacheEntryInfo struct {
	Age   string `json:"age"`
	TTL   string `json:"ttl"`
	State string `json:"state"` // "fresh", "stale", "expired"
}

type CacheGetResponse struct {
	OK    bool   `json:"ok"`
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
	Error string `json:"error,omitempty"`
}

type CacheClearResponse struct {
	OK bool `json:"ok"`
}
