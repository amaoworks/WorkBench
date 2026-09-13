package contracts

const ExternalProtocolVersion = 1

const (
	HealthUnknown      = "unknown"
	HealthReady        = "ready"
	HealthDegraded     = "degraded"
	HealthOffline      = "offline"
	HealthIncompatible = "incompatible"
)

type ExternalPage struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Entry string `json:"entry"`
	Order int    `json:"order"`
}

type ExternalSettingsEntry struct {
	Entry string `json:"entry"`
}

type ExternalManifest struct {
	ID              ModuleID               `json:"id"`
	Name            string                 `json:"name"`
	Version         string                 `json:"version"`
	ProtocolVersion int                    `json:"protocolVersion"`
	Icon            string                 `json:"icon"`
	Capabilities    []string               `json:"capabilities"`
	Pages           []ExternalPage         `json:"pages"`
	Settings        *ExternalSettingsEntry `json:"settings"`
}

type ExternalControlState struct {
	RegistrationID string `json:"registrationId"`
	Generation     int64  `json:"generation"`
	Enabled        bool   `json:"enabled"`
}

type ExternalStatus struct {
	RegistrationID string `json:"registrationId"`
	Generation     int64  `json:"generation"`
	Enabled        bool   `json:"enabled"`
	InstanceID     string `json:"instanceId"`
	Health         string `json:"health"`
	Error          string `json:"error,omitempty"`
}
