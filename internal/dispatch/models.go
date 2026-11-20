package dispatch

import "time"

type RequestState string

const (
	RequestPending   RequestState = "pending"
	RequestAllocated RequestState = "allocated"
	RequestPreparing RequestState = "preparing"
	RequestDialing   RequestState = "dialing"
	RequestActive    RequestState = "active"
	RequestCompleted RequestState = "completed"
	RequestFailed    RequestState = "failed"
)

type LeaseState string

const (
	LeaseReserved        LeaseState = "reserved"
	LeaseSwitching       LeaseState = "switching"
	LeaseIdentityRefresh LeaseState = "identity_refresh"
	LeaseRegistering     LeaseState = "registering"
	LeaseReady           LeaseState = "ready"
	LeaseDialing         LeaseState = "dialing"
	LeaseRinging         LeaseState = "ringing"
	LeaseConnected       LeaseState = "connected"
	LeaseReleasing       LeaseState = "releasing"
	LeaseReleased        LeaseState = "released"
	LeaseFailed          LeaseState = "failed"
	LeaseFaulted         LeaseState = "faulted"
	LeaseOrphaned        LeaseState = "orphaned"
)

type CallRequest struct {
	ID                string       `gorm:"column:id;primaryKey" json:"id"`
	UserID            string       `gorm:"column:user_id;index;not null" json:"user_id"`
	PoolID            string       `gorm:"column:pool_id;index;not null" json:"pool_id"`
	Callee            string       `gorm:"column:callee;not null" json:"callee"`
	State             RequestState `gorm:"column:state;index;not null" json:"state"`
	LeaseID           string       `gorm:"column:lease_id;index" json:"lease_id,omitempty"`
	SelectedDeviceID  string       `gorm:"column:selected_device_id;index" json:"selected_device_id,omitempty"`
	SelectedCardID    string       `gorm:"column:selected_card_id;index" json:"selected_card_id,omitempty"`
	SelectedProfileID string       `gorm:"column:selected_profile_id;index" json:"selected_profile_id,omitempty"`
	SelectedNumberID  string       `gorm:"column:selected_number_id;index" json:"selected_number_id,omitempty"`
	CallID            string       `gorm:"column:call_id;index" json:"call_id,omitempty"`
	FailureCode       string       `gorm:"column:failure_code" json:"failure_code,omitempty"`
	FailureReason     string       `gorm:"column:failure_reason" json:"failure_reason,omitempty"`
	CreatedAt         time.Time    `gorm:"column:created_at;index" json:"created_at"`
	UpdatedAt         time.Time    `gorm:"column:updated_at" json:"updated_at"`
}

type ResourceLease struct {
	ID               string     `gorm:"column:id;primaryKey" json:"id"`
	RequestID        string     `gorm:"column:request_id;uniqueIndex;not null" json:"request_id"`
	UserID           string     `gorm:"column:user_id;index;not null" json:"user_id"`
	PoolID           string     `gorm:"column:pool_id;index;not null" json:"pool_id"`
	DeviceID         string     `gorm:"column:device_id;index;not null" json:"device_id"`
	CardID           string     `gorm:"column:card_id;index;not null" json:"card_id"`
	ProfileID        string     `gorm:"column:profile_id;index;not null" json:"profile_id"`
	NumberID         string     `gorm:"column:number_id;index;not null" json:"number_id"`
	CallID           string     `gorm:"column:call_id;index" json:"call_id,omitempty"`
	State            LeaseState `gorm:"column:state;index;not null" json:"state"`
	Version          uint64     `gorm:"column:version;not null" json:"version"`
	EnforceUserLimit bool       `gorm:"column:enforce_user_limit;not null" json:"enforce_user_limit"`
	AcquiredAt       time.Time  `gorm:"column:acquired_at;index;not null" json:"acquired_at"`
	HeartbeatAt      time.Time  `gorm:"column:heartbeat_at;index;not null" json:"heartbeat_at"`
	ReleasedAt       *time.Time `gorm:"column:released_at;index" json:"released_at,omitempty"`
	FailureCode      string     `gorm:"column:failure_code" json:"failure_code,omitempty"`
	FailureReason    string     `gorm:"column:failure_reason" json:"failure_reason,omitempty"`
	CreatedAt        time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt        time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

type OutcomeKind string

const (
	OutcomeCompleted              OutcomeKind = "completed"
	OutcomeRemoteDeclined         OutcomeKind = "remote_declined"
	OutcomeBusy                   OutcomeKind = "busy"
	OutcomeNoAnswer               OutcomeKind = "no_answer"
	OutcomeTemporarilyUnavailable OutcomeKind = "temporarily_unavailable"
	OutcomeLocalCanceled          OutcomeKind = "local_canceled"
	OutcomeNetworkFailed          OutcomeKind = "network_failed"
	OutcomeMediaFailed            OutcomeKind = "media_failed"
	OutcomeUnknownFailed          OutcomeKind = "unknown_failed"
)

type CallOutcome struct {
	ID           string      `gorm:"column:id;primaryKey" json:"id"`
	CallID       string      `gorm:"column:call_id;uniqueIndex;not null" json:"call_id"`
	RequestID    string      `gorm:"column:request_id;index" json:"request_id,omitempty"`
	LeaseID      string      `gorm:"column:lease_id;index" json:"lease_id,omitempty"`
	UserID       string      `gorm:"column:user_id;index" json:"user_id,omitempty"`
	CardID       string      `gorm:"column:card_id;index" json:"card_id,omitempty"`
	ProfileID    string      `gorm:"column:profile_id;index" json:"profile_id,omitempty"`
	NumberID     string      `gorm:"column:number_id;index" json:"number_id,omitempty"`
	Kind         OutcomeKind `gorm:"column:kind;index;not null" json:"kind"`
	SIPCode      int         `gorm:"column:sip_code;index" json:"sip_code,omitempty"`
	SIPReason    string      `gorm:"column:sip_reason" json:"sip_reason,omitempty"`
	Terminator   string      `gorm:"column:terminator" json:"terminator,omitempty"`
	Counted      bool        `gorm:"column:counted;not null" json:"counted"`
	BusinessDate string      `gorm:"column:business_date;index" json:"business_date,omitempty"`
	ObservedAt   time.Time   `gorm:"column:observed_at;index;not null" json:"observed_at"`
	CreatedAt    time.Time   `gorm:"column:created_at" json:"created_at"`
}

type CardDailyStat struct {
	CardID             string     `gorm:"column:card_id;primaryKey" json:"card_id"`
	BusinessDate       string     `gorm:"column:business_date;primaryKey" json:"business_date"`
	CallCount          int        `gorm:"column:call_count;not null" json:"call_count"`
	RemoteDeclineCount int        `gorm:"column:remote_decline_count;not null" json:"remote_decline_count"`
	DisabledAt         *time.Time `gorm:"column:disabled_at" json:"disabled_at,omitempty"`
	DisabledReason     string     `gorm:"column:disabled_reason" json:"disabled_reason,omitempty"`
	DisabledBy         string     `gorm:"column:disabled_by" json:"disabled_by,omitempty"`
	LastOutcomeID      string     `gorm:"column:last_outcome_id" json:"last_outcome_id,omitempty"`
	CreatedAt          time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt          time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

// MaintenanceInput acquires the same per-device lease used by calls for an
// explicit administrative operation such as a manual eUICC profile switch.
// It prevents a call from being allocated while hardware identity is changing.
type MaintenanceInput struct {
	UserID    string
	Operation string
	DeviceID  string
	CardID    string
	ProfileID string
}
