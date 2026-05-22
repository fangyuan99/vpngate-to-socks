package runner

import "time"

type State string

const (
	StateDisconnected  State = "disconnected"
	StateConnecting    State = "connecting"
	StateConnected     State = "connected"
	StateDisconnecting State = "disconnecting"
	StateFailed        State = "failed"
)

type ConnectionInfo struct {
	HostName     string `json:"hostName"`
	IP           string `json:"ip"`
	CountryLong  string `json:"countryLong,omitempty"`
	CountryShort string `json:"countryShort,omitempty"`
}

type AutoReconnectConfig struct {
	Enabled          bool          `json:"enabled"`
	PreferredCountry string        `json:"preferredCountry,omitempty"`
	MonitorInterval  time.Duration `json:"monitorInterval"`
}

type AutoReconnectStatus struct {
	Enabled                bool   `json:"enabled"`
	Paused                 bool   `json:"paused"`
	PreferredCountry       string `json:"preferredCountry,omitempty"`
	MonitorIntervalSeconds int    `json:"monitorIntervalSeconds"`
}

type Status struct {
	State           State               `json:"state"`
	Current         *ConnectionInfo     `json:"current,omitempty"`
	SocksListenAddr string              `json:"socksListenAddr"`
	LastError       string              `json:"lastError,omitempty"`
	ConnectedAt     time.Time           `json:"connectedAt,omitempty"`
	UpdatedAt       time.Time           `json:"updatedAt"`
	LogTail         []string            `json:"logTail,omitempty"`
	AutoReconnect   AutoReconnectStatus `json:"autoReconnect"`
}
