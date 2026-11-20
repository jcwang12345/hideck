package resourcecatalog

import "time"

// CardContainerType describes the physical card container independently from
// any subscription profile currently selected on it.
type CardContainerType string

const (
	CardContainerTypePhysicalUICC CardContainerType = "physical_uicc"
	CardContainerTypeEUICC        CardContainerType = "euicc"
	CardContainerTypeUnknown      CardContainerType = "unknown"
)

// CardContainer is a durable identity for a UICC/eUICC container. ICCID is
// deliberately absent: ICCIDs belong to SubscriptionProfile and must never be
// used as the durable identity of an eUICC.
type CardContainer struct {
	ID                       string                `gorm:"column:id;primaryKey;size:64" json:"id"`
	Type                     CardContainerType     `gorm:"column:container_type;size:32;not null;index" json:"type"`
	EID                      *string               `gorm:"column:eid;size:64;uniqueIndex" json:"eid,omitempty"`
	LocatorID                *string               `gorm:"column:locator_id;size:64;uniqueIndex" json:"-"`
	LastDeviceID             string                `gorm:"column:last_device_id;size:128;index" json:"last_device_id,omitempty"`
	LastSlotID               string                `gorm:"column:last_slot_id;size:128" json:"last_slot_id,omitempty"`
	ISDRObserved             bool                  `gorm:"column:isdr_observed;not null" json:"isdr_observed"`
	MultipleProfilesObserved bool                  `gorm:"column:multiple_profiles_observed;not null" json:"multiple_profiles_observed"`
	ObservedProfileCount     int                   `gorm:"column:observed_profile_count;not null" json:"observed_profile_count"`
	LastObservedAt           time.Time             `gorm:"column:last_observed_at;not null;index" json:"last_observed_at"`
	CreatedAt                time.Time             `gorm:"column:created_at" json:"created_at"`
	UpdatedAt                time.Time             `gorm:"column:updated_at" json:"updated_at"`
	Profiles                 []SubscriptionProfile `gorm:"foreignKey:CardContainerID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"-"`
}

func (CardContainer) TableName() string { return "resource_card_containers" }

// SubscriptionProfile is a subscription carried by a CardContainer. A
// container can retain several historical or currently installed profiles.
type SubscriptionProfile struct {
	ID              string    `gorm:"column:id;primaryKey;size:64" json:"id"`
	CardContainerID string    `gorm:"column:card_container_id;size:64;not null;index;uniqueIndex:idx_resource_profile_identity,priority:1" json:"card_container_id"`
	IdentityKey     string    `gorm:"column:identity_key;size:160;not null;uniqueIndex:idx_resource_profile_identity,priority:2" json:"-"`
	ICCID           string    `gorm:"column:iccid;size:32;index" json:"iccid,omitempty"`
	AID             string    `gorm:"column:aid;size:128;index" json:"aid,omitempty"`
	IMSI            string    `gorm:"column:imsi;size:32;index" json:"imsi,omitempty"`
	Name            string    `gorm:"column:name;size:128" json:"name,omitempty"`
	ServiceProvider string    `gorm:"column:service_provider;size:128" json:"service_provider,omitempty"`
	Present         bool      `gorm:"column:present;not null" json:"present"`
	Active          bool      `gorm:"column:active;not null" json:"active"`
	LastObservedAt  time.Time `gorm:"column:last_observed_at;not null;index" json:"last_observed_at"`
	CreatedAt       time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt       time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (SubscriptionProfile) TableName() string { return "resource_subscription_profiles" }

// PhoneNumber is a callable number assigned to a subscription profile.
type PhoneNumber struct {
	ID                    string    `gorm:"column:id;primaryKey;size:64" json:"id"`
	SubscriptionProfileID *string   `gorm:"column:subscription_profile_id;size:64;index" json:"subscription_profile_id,omitempty"`
	E164                  string    `gorm:"column:e164;size:32;not null;uniqueIndex" json:"e164"`
	DisplayNumber         string    `gorm:"column:display_number;size:64" json:"display_number,omitempty"`
	Label                 string    `gorm:"column:label;size:128" json:"label,omitempty"`
	Status                string    `gorm:"column:status;size:32;index" json:"status,omitempty"`
	CreatedAt             time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt             time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (PhoneNumber) TableName() string { return "resource_phone_numbers" }

// NumberPool groups phone numbers for inbound or outbound dispatch.
type NumberPool struct {
	ID          string    `gorm:"column:id;primaryKey;size:64" json:"id"`
	Name        string    `gorm:"column:name;size:128;not null;uniqueIndex" json:"name"`
	Description string    `gorm:"column:description;size:512" json:"description,omitempty"`
	Enabled     bool      `gorm:"column:enabled;not null;index" json:"enabled"`
	CreatedAt   time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (NumberPool) TableName() string { return "resource_number_pools" }

// NumberPoolMember associates a phone number with a dispatch pool.
type NumberPoolMember struct {
	ID            string    `gorm:"column:id;primaryKey;size:64" json:"id"`
	NumberPoolID  string    `gorm:"column:number_pool_id;size:64;not null;index;uniqueIndex:idx_resource_pool_member,priority:1" json:"number_pool_id"`
	PhoneNumberID string    `gorm:"column:phone_number_id;size:64;not null;index;uniqueIndex:idx_resource_pool_member,priority:2" json:"phone_number_id"`
	Priority      int       `gorm:"column:priority;not null;index" json:"priority"`
	Weight        int       `gorm:"column:weight;not null" json:"weight"`
	Enabled       bool      `gorm:"column:enabled;not null;index" json:"enabled"`
	CreatedAt     time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt     time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (NumberPoolMember) TableName() string { return "resource_number_pool_members" }

// NumberPoolUser grants a user access to a number pool.
type NumberPoolUser struct {
	ID           string    `gorm:"column:id;primaryKey;size:64" json:"id"`
	NumberPoolID string    `gorm:"column:number_pool_id;size:64;not null;index;uniqueIndex:idx_resource_pool_user,priority:1" json:"number_pool_id"`
	UserID       string    `gorm:"column:user_id;size:128;not null;index;uniqueIndex:idx_resource_pool_user,priority:2" json:"user_id"`
	Role         string    `gorm:"column:role;size:32;not null" json:"role"`
	Enabled      bool      `gorm:"column:enabled;not null;index" json:"enabled"`
	CreatedAt    time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt    time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (NumberPoolUser) TableName() string { return "resource_number_pool_users" }

// DispatchPolicy defines how a number pool selects one of its members.
type DispatchPolicy struct {
	ID           string    `gorm:"column:id;primaryKey;size:64" json:"id"`
	NumberPoolID string    `gorm:"column:number_pool_id;size:64;not null;index" json:"number_pool_id"`
	Name         string    `gorm:"column:name;size:128;not null" json:"name"`
	Strategy     string    `gorm:"column:strategy;size:64;not null;index" json:"strategy"`
	Priority     int       `gorm:"column:priority;not null;index" json:"priority"`
	Enabled      bool      `gorm:"column:enabled;not null;index" json:"enabled"`
	ConfigJSON   string    `gorm:"column:config_json;type:text" json:"config_json,omitempty"`
	CreatedAt    time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt    time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (DispatchPolicy) TableName() string { return "resource_dispatch_policies" }

// AuditEvent is an append-only record of a resource catalog change.
type AuditEvent struct {
	ID           string    `gorm:"column:id;primaryKey;size:64" json:"id"`
	ActorID      string    `gorm:"column:actor_id;size:128;index" json:"actor_id,omitempty"`
	Action       string    `gorm:"column:action;size:64;not null;index" json:"action"`
	ResourceType string    `gorm:"column:resource_type;size:64;not null;index:idx_resource_audit_target,priority:1" json:"resource_type"`
	ResourceID   string    `gorm:"column:resource_id;size:128;not null;index:idx_resource_audit_target,priority:2" json:"resource_id"`
	PayloadJSON  string    `gorm:"column:payload_json;type:text" json:"payload_json,omitempty"`
	OccurredAt   time.Time `gorm:"column:occurred_at;not null;index" json:"occurred_at"`
	CreatedAt    time.Time `gorm:"column:created_at" json:"created_at"`
}

func (AuditEvent) TableName() string { return "resource_audit_events" }
