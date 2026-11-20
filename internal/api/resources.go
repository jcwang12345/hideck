package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yibaiba/hideck/internal/db"
	"github.com/yibaiba/hideck/internal/dispatch"
	"github.com/yibaiba/hideck/internal/esim"
	"github.com/yibaiba/hideck/internal/identity"
	"github.com/yibaiba/hideck/internal/phone"
	"github.com/yibaiba/hideck/internal/resourcecatalog"
	"gorm.io/gorm"
)

// registerResourceRoutes installs the resource-management API. Every route
// carries its own authorization rule so the method remains safe if the caller
// ever mounts it on a router group with less restrictive middleware.
func (s *Server) registerResourceRoutes(api *gin.RouterGroup) {
	admin := RequireRole(identity.RoleAdmin)
	authenticated := RequireRole(identity.RoleAdmin, identity.RoleMember)

	api.GET("/cards", admin, s.handleResourceCards)
	api.POST("/cards/:id/disable", admin, s.handleDisableResourceCard)
	api.POST("/cards/:id/enable", admin, s.handleEnableResourceCard)
	api.POST("/cards/:id/profiles/:profile_id/actions/activate", admin, s.handleActivateResourceProfile)
	api.GET("/number-pools", authenticated, s.handleListNumberPools)
	api.POST("/number-pools", admin, s.handleCreateNumberPool)
	api.PATCH("/number-pools/:id", admin, s.handleUpdateNumberPool)
	api.PUT("/number-pools/:id/members", admin, s.handleReplaceNumberPoolMembers)
	api.PUT("/number-pools/:id/users", admin, s.handleReplaceNumberPoolUsers)
	api.GET("/phone-numbers", admin, s.handleListPhoneNumbers)
	api.POST("/phone-numbers", admin, s.handleUpsertPhoneNumber)
	api.GET("/dispatch-policies", admin, s.handleListDispatchPolicies)
	api.POST("/dispatch-policies", admin, s.handleUpsertDispatchPolicy)
	api.GET("/call-records", authenticated, s.handleResourceCallRecords)
	api.GET("/audit-events", admin, s.handleResourceAuditEvents)
}

func (s *Server) requireResourceCatalog(c *gin.Context) (*resourcecatalog.Service, bool) {
	if s.resourceInitErr != nil || s.resourceCatalog == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "error", "code": "resource_unavailable", "message": "资源目录服务不可用", "request_id": requestID(c),
		})
		return nil, false
	}
	return s.resourceCatalog, true
}

type cardResourceResponse struct {
	ID                string                                   `json:"id"`
	CardID            string                                   `json:"card_id"`
	ICCID             string                                   `json:"iccid,omitempty"`
	EID               string                                   `json:"eid,omitempty"`
	Kind              resourcecatalog.CardContainerType        `json:"kind"`
	CardType          resourcecatalog.CardContainerType        `json:"card_type"`
	DeviceID          string                                   `json:"device_id,omitempty"`
	DeviceName        string                                   `json:"device_name,omitempty"`
	ActiveProfileID   string                                   `json:"active_profile_id,omitempty"`
	ActiveProfileName string                                   `json:"active_profile_name,omitempty"`
	ActiveNumber      string                                   `json:"active_number,omitempty"`
	Status            string                                   `json:"status"`
	OccupiedBy        string                                   `json:"occupied_by,omitempty"`
	DailyCalls        int64                                    `json:"daily_calls"`
	DailyRejections   int                                      `json:"daily_rejections"`
	DisabledReason    string                                   `json:"disabled_reason,omitempty"`
	CanSwitchProfile  bool                                     `json:"can_switch_profile"`
	UpdatedAt         time.Time                                `json:"updated_at"`
	Capabilities      resourcecatalog.CardCapability           `json:"capabilities"`
	Profiles          []resourcecatalog.SubscriptionProfileDTO `json:"profiles"`
}

type cachedDeviceResource struct {
	name   string
	online bool
}

func (s *Server) handleResourceCards(c *gin.Context) {
	service, ok := s.requireResourceCatalog(c)
	if !ok {
		return
	}
	if err := s.syncCachedCardResources(c, service); err != nil {
		writeResourceError(c, err)
		return
	}
	cards, err := service.List(c.Request.Context())
	if err != nil {
		writeResourceError(c, err)
		return
	}
	numbers, err := service.ListPhoneNumbers(c.Request.Context())
	if err != nil {
		writeResourceError(c, err)
		return
	}
	numberByProfile := make(map[string]string, len(numbers))
	for _, number := range numbers {
		if number.SubscriptionProfileID != nil && strings.TrimSpace(*number.SubscriptionProfileID) != "" {
			numberByProfile[*number.SubscriptionProfileID] = number.E164
		}
	}
	devices := s.cachedDeviceResources()
	if s.dispatcher == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "error", "code": "resource_unavailable", "message": "调度服务不可用", "request_id": requestID(c),
		})
		return
	}
	stats, err := s.dispatcher.DailyStats(c.Request.Context(), "")
	if err != nil {
		writeResourceError(c, err)
		return
	}
	statsByCard := make(map[string]dispatch.CardDailyStat, len(stats))
	for _, stat := range stats {
		statsByCard[stat.CardID] = stat
	}
	occupiedByCard := map[string]string{}
	if leases, leaseErr := s.dispatcher.ActiveLeases(c.Request.Context()); leaseErr == nil {
		for _, lease := range leases {
			occupiedByCard[lease.CardID] = lease.UserID
		}
	}
	userNames := s.resourceUserNames(c)
	result := make([]cardResourceResponse, 0, len(cards))
	for _, card := range cards {
		response := cardResourceResponse{
			ID: card.ID, CardID: card.ID, Kind: card.Type, CardType: card.Type,
			DeviceID: card.LastDeviceID, Status: "offline", UpdatedAt: card.UpdatedAt,
			Capabilities: card.Capabilities, Profiles: card.Profiles,
		}
		if card.EID != nil {
			response.EID = *card.EID
		}
		if device, exists := devices[card.LastDeviceID]; exists {
			response.DeviceName = device.name
			if device.online {
				response.Status = "online"
			}
		}
		if s.pool != nil && response.Status == "online" {
			if s.pool.IsESIMSwitching(card.LastDeviceID) {
				response.Status = "switching"
			} else if runtimeState, exists := s.pool.GetVoWiFiRuntimeState(card.LastDeviceID); exists && !runtimeState.IMSReady {
				if runtimeState.LastError != "" && (runtimeState.IMSState == "failed" || runtimeState.SessionState == "error") {
					response.Status = "faulted"
				} else {
					response.Status = "registering"
				}
			}
		}
		if stat, exists := statsByCard[card.ID]; exists {
			response.DailyCalls = int64(stat.CallCount)
			response.DailyRejections = stat.RemoteDeclineCount
			response.DisabledReason = stat.DisabledReason
			if stat.DisabledAt != nil {
				response.Status = "disabled"
			}
		}
		if userID := occupiedByCard[card.ID]; userID != "" {
			response.OccupiedBy = userNames[userID]
			if response.OccupiedBy == "" {
				response.OccupiedBy = userID
			}
			if response.Status != "disabled" {
				response.Status = "occupied"
			}
		}
		presentProfiles := 0
		for _, profile := range card.Profiles {
			if profile.Present {
				presentProfiles++
			}
			if !profile.Active || response.ActiveProfileID != "" {
				continue
			}
			response.ActiveProfileID = profile.ID
			response.ActiveProfileName = profile.Name
			response.ICCID = profile.ICCID
			response.ActiveNumber = numberByProfile[profile.ID]
		}
		response.CanSwitchProfile = card.Type == resourcecatalog.CardContainerTypeEUICC && presentProfiles > 1
		result = append(result, response)
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "cards": result})
}

type disableResourceCardRequest struct {
	Reason string `json:"reason"`
}

func (s *Server) handleDisableResourceCard(c *gin.Context) {
	service, ok := s.requireResourceCatalog(c)
	if !ok {
		return
	}
	if s.dispatcher == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "error", "code": "resource_unavailable", "message": "调度服务不可用", "request_id": requestID(c),
		})
		return
	}
	cardID := strings.TrimSpace(c.Param("id"))
	if _, err := service.Get(c.Request.Context(), cardID); err != nil {
		writeResourceError(c, err)
		return
	}
	var request disableResourceCardRequest
	if !decodeOptionalResourceJSON(c, &request) {
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if len([]rune(request.Reason)) > 256 {
		writeResourceRequestError(c, errors.New("reason must not exceed 256 characters"))
		return
	}
	principal, _ := CurrentPrincipal(c)
	wasDisabled, previous, err := s.dispatcher.CardDisabledToday(c.Request.Context(), cardID)
	if err != nil {
		writeResourceError(c, err)
		return
	}
	stat, err := s.dispatcher.DisableCardToday(c.Request.Context(), cardID, principal.UserID, request.Reason)
	if err != nil {
		writeResourceError(c, err)
		return
	}
	if err := service.RecordAudit(c.Request.Context(), principal.UserID, "card.manually_disabled", "card", cardID, gin.H{
		"reason": stat.DisabledReason, "business_date": stat.BusinessDate,
	}); err != nil {
		compensateCardDisable(c, s, cardID, wasDisabled, previous)
		writeResourceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "card_id": cardID, "daily_stat": stat})
}

func (s *Server) handleEnableResourceCard(c *gin.Context) {
	service, ok := s.requireResourceCatalog(c)
	if !ok {
		return
	}
	if s.dispatcher == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "error", "code": "resource_unavailable", "message": "调度服务不可用", "request_id": requestID(c),
		})
		return
	}
	cardID := strings.TrimSpace(c.Param("id"))
	if _, err := service.Get(c.Request.Context(), cardID); err != nil {
		writeResourceError(c, err)
		return
	}
	var request struct{}
	if !decodeOptionalResourceJSON(c, &request) {
		return
	}
	principal, _ := CurrentPrincipal(c)
	wasDisabled, previous, err := s.dispatcher.CardDisabledToday(c.Request.Context(), cardID)
	if err != nil {
		writeResourceError(c, err)
		return
	}
	stat, err := s.dispatcher.EnableCardToday(c.Request.Context(), cardID)
	if err != nil {
		writeResourceError(c, err)
		return
	}
	if err := service.RecordAudit(c.Request.Context(), principal.UserID, "card.manually_enabled", "card", cardID, gin.H{
		"business_date": stat.BusinessDate,
	}); err != nil {
		if wasDisabled {
			_, _ = s.dispatcher.DisableCardToday(c.Request.Context(), cardID, previous.DisabledBy, previous.DisabledReason)
		}
		writeResourceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "card_id": cardID, "daily_stat": stat})
}

func compensateCardDisable(c *gin.Context, s *Server, cardID string, wasDisabled bool, previous dispatch.CardDailyStat) {
	if wasDisabled {
		_, _ = s.dispatcher.DisableCardToday(c.Request.Context(), cardID, previous.DisabledBy, previous.DisabledReason)
		return
	}
	_, _ = s.dispatcher.EnableCardToday(c.Request.Context(), cardID)
}

// syncCachedCardResources consumes only data already held by workers and the
// eSIM manager. CachedProfileGroups, GetCachedDeviceStatus, CurrentICCID and
// GetCachedIMSI do not open a logical channel, issue APDUs, or trigger scans.
func (s *Server) syncCachedCardResources(c *gin.Context, service *resourcecatalog.Service) error {
	if s.pool == nil {
		return nil
	}
	observedAt := time.Now().UTC()
	for _, worker := range s.pool.GetAllWorkers() {
		if worker == nil {
			continue
		}
		status := worker.GetCachedDeviceStatus()
		currentICCID := strings.TrimSpace(worker.CurrentICCID())
		currentIMSI := strings.TrimSpace(worker.GetCachedIMSI())
		var groups []esim.EUICCProfiles
		physicalUICCConfirmed := false
		if worker.EsimMgr != nil {
			groups = worker.EsimMgr.CachedProfileGroups()
			_, physicalUICCConfirmed = worker.EsimMgr.CachedCardTypeEvidence()
		}
		if len(groups) > 0 {
			for _, group := range groups {
				profiles := make([]resourcecatalog.ProfileObservation, 0, len(group.Profiles))
				for _, profile := range group.Profiles {
					imsi := ""
					isCurrent := sameCardIdentity(profile.ICCID, currentICCID)
					if isCurrent {
						imsi = currentIMSI
					}
					active := profile.State == 1
					if currentICCID != "" {
						active = isCurrent
					}
					profiles = append(profiles, resourcecatalog.ProfileObservation{
						ICCID: profile.ICCID, AID: group.AIDHex, IMSI: imsi,
						Name: profile.Name, ServiceProviderName: profile.ServiceProviderName,
						Active: active,
					})
				}
				observation := resourcecatalog.CardObservation{
					StableContainerKey: cachedCardLocator(worker.ID, group.AIDHex),
					DeviceID:           worker.ID, SlotID: strings.TrimSpace(group.AIDHex),
					CardPresent: true, EID: group.EID, ISDRAID: group.AIDHex,
					Profiles: profiles, ProfilesComplete: true, ObservedAt: observedAt,
				}
				if _, err := service.SyncObservation(c.Request.Context(), observation); err != nil {
					return err
				}
			}
			continue
		}
		if !status.SimInserted && currentICCID == "" {
			continue
		}
		profiles := []resourcecatalog.ProfileObservation(nil)
		if currentICCID != "" || currentIMSI != "" {
			profiles = []resourcecatalog.ProfileObservation{{ICCID: currentICCID, IMSI: currentIMSI, Active: true}}
		}
		_, err := service.SyncObservation(c.Request.Context(), resourcecatalog.CardObservation{
			StableContainerKey: cachedCardLocator(worker.ID, "primary"),
			DeviceID:           worker.ID, SlotID: "primary", CardPresent: true,
			// A cached SIM-present bit is not proof of a physical UICC. Keep the
			// type unknown until the card layer supplies explicit evidence.
			PhysicalUICCConfirmed: physicalUICCConfirmed,
			Profiles:              profiles, ProfilesComplete: profiles != nil, ObservedAt: observedAt,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func cachedCardLocator(deviceID, slot string) string {
	return "worker:" + strings.TrimSpace(deviceID) + "/slot:" + strings.TrimSpace(slot)
}

func sameCardIdentity(left, right string) bool {
	normalize := func(value string) string {
		return strings.NewReplacer(" ", "", "-", "", ":", "").Replace(strings.ToUpper(strings.TrimSpace(value)))
	}
	return normalize(left) != "" && normalize(left) == normalize(right)
}

func (s *Server) cachedDeviceResources() map[string]cachedDeviceResource {
	result := map[string]cachedDeviceResource{}
	if s.pool == nil {
		return result
	}
	for _, worker := range s.pool.GetAllWorkers() {
		if worker == nil {
			continue
		}
		name := strings.TrimSpace(worker.Config.Name)
		if name == "" {
			name = worker.ID
		}
		result[worker.ID] = cachedDeviceResource{name: name, online: true}
	}
	return result
}

type numberPoolResponse struct {
	resourcecatalog.NumberPoolView
	Status              string `json:"status"`
	MemberCount         int    `json:"member_count"`
	AvailableCount      int    `json:"available_count"`
	OccupiedCount       int    `json:"occupied_count"`
	AuthorizedUserCount int    `json:"authorized_user_count"`
}

func (s *Server) mapNumberPool(c *gin.Context, view resourcecatalog.NumberPoolView, admin bool) numberPoolResponse {
	status := "disabled"
	if view.Enabled {
		status = "enabled"
	}
	response := numberPoolResponse{NumberPoolView: view, Status: status, MemberCount: len(view.Members)}
	if !admin {
		response.Users = nil
		response.Policies = nil
		for index := range response.Members {
			if response.Members[index].Profile != nil {
				profile := *response.Members[index].Profile
				profile.ICCID, profile.IMSI, profile.AID = "", "", ""
				response.Members[index].Profile = &profile
			}
			if response.Members[index].Card != nil {
				card := *response.Members[index].Card
				card.EID, card.LocatorID = nil, nil
				response.Members[index].Card = &card
			}
		}
	}
	if s.dispatcher != nil && s.pool != nil {
		if candidates, err := s.dispatchCandidates(c.Request.Context(), view); err == nil {
			for _, candidate := range candidates {
				if candidate.Busy {
					response.OccupiedCount++
					continue
				}
				disabled, _, disabledErr := s.dispatcher.CardDisabledToday(c.Request.Context(), candidate.CardID)
				if disabledErr == nil && !disabled && candidate.Online && candidate.Enabled && !candidate.Faulted && !candidate.Switching && !candidate.Registering {
					response.AvailableCount++
				}
			}
		}
	}
	for _, user := range view.Users {
		if user.Enabled {
			response.AuthorizedUserCount++
		}
	}
	return response
}

func (s *Server) handleListNumberPools(c *gin.Context) {
	service, ok := s.requireResourceCatalog(c)
	if !ok {
		return
	}
	principal, ok := CurrentPrincipal(c)
	if !ok {
		abortAuthorization(c, http.StatusUnauthorized, "unauthorized", "未授权")
		return
	}
	admin := principal.Role == identity.RoleAdmin
	views, err := service.ListNumberPools(c.Request.Context(), principal.UserID, admin)
	if err != nil {
		writeResourceError(c, err)
		return
	}
	result := make([]numberPoolResponse, 0, len(views))
	for _, view := range views {
		result = append(result, s.mapNumberPool(c, view, admin))
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "number_pools": result})
}

type createNumberPoolRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     *bool  `json:"enabled"`
}

func (s *Server) handleCreateNumberPool(c *gin.Context) {
	service, ok := s.requireResourceCatalog(c)
	if !ok {
		return
	}
	var request createNumberPoolRequest
	if !decodeResourceJSON(c, &request) {
		return
	}
	principal, _ := CurrentPrincipal(c)
	pool, err := service.CreateNumberPool(c.Request.Context(), principal.UserID, resourcecatalog.CreateNumberPoolInput{
		Name: request.Name, Description: request.Description, Enabled: request.Enabled,
	})
	if err != nil {
		writeResourceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"status": "ok", "number_pool": pool})
}

type updateNumberPoolRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Enabled     *bool   `json:"enabled"`
}

func (s *Server) handleUpdateNumberPool(c *gin.Context) {
	service, ok := s.requireResourceCatalog(c)
	if !ok {
		return
	}
	var request updateNumberPoolRequest
	if !decodeResourceJSON(c, &request) {
		return
	}
	if request.Name == nil && request.Description == nil && request.Enabled == nil {
		writeResourceRequestError(c, errors.New("at least one field is required"))
		return
	}
	principal, _ := CurrentPrincipal(c)
	pool, err := service.UpdateNumberPool(c.Request.Context(), principal.UserID, c.Param("id"), resourcecatalog.UpdateNumberPoolInput{
		Name: request.Name, Description: request.Description, Enabled: request.Enabled,
	})
	if err != nil {
		writeResourceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "number_pool": pool})
}

type replacePoolMembersRequest struct {
	Members []resourcecatalog.PoolMemberInput `json:"members"`
}

func (s *Server) handleReplaceNumberPoolMembers(c *gin.Context) {
	service, ok := s.requireResourceCatalog(c)
	if !ok {
		return
	}
	var request replacePoolMembersRequest
	if !decodeResourceJSON(c, &request) {
		return
	}
	if request.Members == nil {
		writeResourceRequestError(c, errors.New("members is required"))
		return
	}
	principal, _ := CurrentPrincipal(c)
	members, err := service.ReplacePoolMembers(c.Request.Context(), principal.UserID, c.Param("id"), request.Members)
	if err != nil {
		writeResourceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "members": members})
}

type replacePoolUsersRequest struct {
	Users []resourcecatalog.PoolUserInput `json:"users"`
}

func (s *Server) handleReplaceNumberPoolUsers(c *gin.Context) {
	service, ok := s.requireResourceCatalog(c)
	if !ok {
		return
	}
	var request replacePoolUsersRequest
	if !decodeResourceJSON(c, &request) {
		return
	}
	if request.Users == nil {
		writeResourceRequestError(c, errors.New("users is required"))
		return
	}
	if s.identityInitErr != nil || s.identityService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "error", "code": "identity_unavailable", "message": "身份服务不可用", "request_id": requestID(c),
		})
		return
	}
	for index, grant := range request.Users {
		userID := strings.TrimSpace(grant.UserID)
		if userID == "" {
			writeResourceRequestError(c, fmt.Errorf("user grant %d has no user", index))
			return
		}
		if _, err := s.identityService.GetUser(c.Request.Context(), userID); err != nil {
			if errors.Is(err, identity.ErrUserNotFound) {
				writeResourceRequestError(c, fmt.Errorf("user %s does not exist", userID))
				return
			}
			writeIdentityError(c, err)
			return
		}
	}
	principal, _ := CurrentPrincipal(c)
	users, err := service.ReplacePoolUsers(c.Request.Context(), principal.UserID, c.Param("id"), request.Users)
	if err != nil {
		writeResourceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "users": users})
}

func (s *Server) handleListPhoneNumbers(c *gin.Context) {
	service, ok := s.requireResourceCatalog(c)
	if !ok {
		return
	}
	numbers, err := service.ListPhoneNumbers(c.Request.Context())
	if err != nil {
		writeResourceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "phone_numbers": numbers})
}

type upsertPhoneNumberRequest struct {
	ID                    string `json:"id"`
	SubscriptionProfileID string `json:"subscription_profile_id"`
	E164                  string `json:"e164"`
	DisplayNumber         string `json:"display_number"`
	Label                 string `json:"label"`
	Status                string `json:"status"`
}

func (s *Server) handleUpsertPhoneNumber(c *gin.Context) {
	service, ok := s.requireResourceCatalog(c)
	if !ok {
		return
	}
	var request upsertPhoneNumberRequest
	if !decodeResourceJSON(c, &request) {
		return
	}
	principal, _ := CurrentPrincipal(c)
	number, err := service.UpsertPhoneNumber(c.Request.Context(), principal.UserID, resourcecatalog.PhoneNumberInput{
		ID: request.ID, SubscriptionProfileID: request.SubscriptionProfileID,
		E164: request.E164, DisplayNumber: request.DisplayNumber, Label: request.Label, Status: request.Status,
	})
	if err != nil {
		writeResourceError(c, err)
		return
	}
	status := http.StatusCreated
	if strings.TrimSpace(request.ID) != "" {
		status = http.StatusOK
	}
	c.JSON(status, gin.H{"status": "ok", "phone_number": number})
}

type dispatchPolicyResponse struct {
	resourcecatalog.DispatchPolicy
	Status             string `json:"status"`
	PoolName           string `json:"pool_name,omitempty"`
	RejectionThreshold int    `json:"rejection_threshold,omitempty"`
	DisableScope       string `json:"disable_scope,omitempty"`
	Timezone           string `json:"timezone,omitempty"`
	RestoreTime        string `json:"restore_time,omitempty"`
}

func (s *Server) handleListDispatchPolicies(c *gin.Context) {
	service, ok := s.requireResourceCatalog(c)
	if !ok {
		return
	}
	policies, err := service.ListDispatchPolicies(c.Request.Context())
	if err != nil {
		writeResourceError(c, err)
		return
	}
	pools, err := service.ListNumberPools(c.Request.Context(), "", true)
	if err != nil {
		writeResourceError(c, err)
		return
	}
	poolNames := make(map[string]string, len(pools))
	for _, pool := range pools {
		poolNames[pool.ID] = pool.Name
	}
	result := make([]dispatchPolicyResponse, 0, len(policies))
	for _, policy := range policies {
		result = append(result, mapDispatchPolicy(policy, poolNames[policy.NumberPoolID]))
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "dispatch_policies": result})
}

type upsertDispatchPolicyRequest struct {
	ID           string          `json:"id"`
	NumberPoolID string          `json:"number_pool_id"`
	Name         string          `json:"name"`
	Strategy     string          `json:"strategy"`
	Priority     int             `json:"priority"`
	Enabled      *bool           `json:"enabled"`
	ConfigJSON   string          `json:"config_json"`
	Config       json.RawMessage `json:"config"`
}

func (s *Server) handleUpsertDispatchPolicy(c *gin.Context) {
	service, ok := s.requireResourceCatalog(c)
	if !ok {
		return
	}
	var request upsertDispatchPolicyRequest
	if !decodeResourceJSON(c, &request) {
		return
	}
	configJSON := strings.TrimSpace(request.ConfigJSON)
	if len(request.Config) > 0 && string(request.Config) != "null" {
		if configJSON != "" {
			writeResourceRequestError(c, errors.New("use either config or config_json"))
			return
		}
		if !json.Valid(request.Config) {
			writeResourceRequestError(c, errors.New("config must be valid JSON"))
			return
		}
		configJSON = string(request.Config)
	}
	principal, _ := CurrentPrincipal(c)
	policy, err := service.UpsertDispatchPolicy(c.Request.Context(), principal.UserID, resourcecatalog.DispatchPolicyInput{
		ID: request.ID, NumberPoolID: request.NumberPoolID, Name: request.Name,
		Strategy: request.Strategy, Priority: request.Priority, Enabled: request.Enabled, ConfigJSON: configJSON,
	})
	if err != nil {
		writeResourceError(c, err)
		return
	}
	status := http.StatusCreated
	if strings.TrimSpace(request.ID) != "" {
		status = http.StatusOK
	}
	c.JSON(status, gin.H{"status": "ok", "dispatch_policy": mapDispatchPolicy(policy, "")})
}

func mapDispatchPolicy(policy resourcecatalog.DispatchPolicy, poolName string) dispatchPolicyResponse {
	status := "disabled"
	if policy.Enabled {
		status = "enabled"
	}
	response := dispatchPolicyResponse{DispatchPolicy: policy, Status: status, PoolName: poolName}
	var config map[string]any
	decoder := json.NewDecoder(strings.NewReader(policy.ConfigJSON))
	decoder.UseNumber()
	if err := decoder.Decode(&config); err != nil {
		return response
	}
	response.RejectionThreshold = jsonInt(config["rejection_threshold"])
	response.DisableScope = jsonString(config["disable_scope"])
	response.Timezone = jsonString(config["timezone"])
	response.RestoreTime = jsonString(config["restore_time"])
	return response
}

type callRecordResponse struct {
	ID              uint64     `json:"id"`
	CallID          string     `json:"call_id"`
	UserID          string     `json:"user_id,omitempty"`
	Username        string     `json:"username,omitempty"`
	Direction       string     `json:"direction"`
	Peer            string     `json:"peer"`
	Status          string     `json:"status"`
	StartedAt       time.Time  `json:"started_at"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
	DurationSeconds int64      `json:"duration_seconds"`
	PoolName        string     `json:"pool_name,omitempty"`
	LocalNumber     string     `json:"local_number,omitempty"`
	DeviceID        string     `json:"device_id"`
	EndReason       string     `json:"end_reason,omitempty"`
}

func (s *Server) handleResourceCallRecords(c *gin.Context) {
	if s.resourceInitErr != nil || s.dispatcher == nil || db.DB == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "error", "code": "resource_unavailable", "message": "通话资源服务不可用", "request_id": requestID(c),
		})
		return
	}
	principal, ok := CurrentPrincipal(c)
	if !ok {
		abortAuthorization(c, http.StatusUnauthorized, "unauthorized", "未授权")
		return
	}
	limit, err := resourceListLimit(c.Query("limit"))
	if err != nil {
		writeResourceRequestError(c, err)
		return
	}
	store := db.NewVoiceCallStore(db.DB)
	var records []phone.CallRecord
	if principal.Role == identity.RoleAdmin {
		records, err = store.List(c.Request.Context(), limit)
	} else {
		records, err = store.ListByUser(c.Request.Context(), principal.UserID, limit)
	}
	if err != nil {
		writeResourceError(c, err)
		return
	}
	userNames := s.resourceUserNames(c)
	poolNames, numberValues := s.resourceNames(c)
	result := make([]callRecordResponse, 0, len(records))
	for _, record := range records {
		localNumber := record.SourceNumber
		if localNumber == "" {
			localNumber = numberValues[record.NumberID]
		}
		result = append(result, callRecordResponse{
			ID: record.ID, CallID: record.CallID, UserID: record.UserID, Username: userNames[record.UserID],
			Direction: record.Direction, Peer: record.Peer, Status: record.Status,
			StartedAt: record.StartedAt, EndedAt: record.EndedAt, DurationSeconds: record.DurationSeconds,
			PoolName: poolNames[record.PoolID], LocalNumber: localNumber,
			DeviceID: record.DeviceID, EndReason: record.EndReason,
		})
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "call_records": result})
}

type auditEventResponse struct {
	ID           string    `json:"id"`
	ActorID      string    `json:"actor_id,omitempty"`
	ActorName    string    `json:"actor_name,omitempty"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id"`
	Result       string    `json:"result"`
	Message      string    `json:"message,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

func (s *Server) handleResourceAuditEvents(c *gin.Context) {
	service, ok := s.requireResourceCatalog(c)
	if !ok {
		return
	}
	limit, err := resourceListLimit(c.Query("limit"))
	if err != nil {
		writeResourceRequestError(c, err)
		return
	}
	events, err := service.ListAuditEvents(c.Request.Context(), limit)
	if err != nil {
		writeResourceError(c, err)
		return
	}
	userNames := s.resourceUserNames(c)
	result := make([]auditEventResponse, 0, len(events))
	for _, event := range events {
		result = append(result, auditEventResponse{
			ID: event.ID, ActorID: event.ActorID, ActorName: userNames[event.ActorID],
			Action: event.Action, ResourceType: event.ResourceType, ResourceID: event.ResourceID,
			Result: "success", CreatedAt: event.OccurredAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "audit_events": result})
}

func (s *Server) resourceUserNames(c *gin.Context) map[string]string {
	result := map[string]string{}
	if s.identityService == nil {
		return result
	}
	users, err := s.identityService.ListUsers(c.Request.Context())
	if err != nil {
		return result
	}
	for _, user := range users {
		result[user.ID] = user.Username
	}
	return result
}

func (s *Server) resourceNames(c *gin.Context) (map[string]string, map[string]string) {
	poolNames, numberValues := map[string]string{}, map[string]string{}
	if s.resourceCatalog == nil {
		return poolNames, numberValues
	}
	if pools, err := s.resourceCatalog.ListNumberPools(c.Request.Context(), "", true); err == nil {
		for _, pool := range pools {
			poolNames[pool.ID] = pool.Name
		}
	}
	if numbers, err := s.resourceCatalog.ListPhoneNumbers(c.Request.Context()); err == nil {
		for _, number := range numbers {
			numberValues[number.ID] = number.E164
		}
	}
	return poolNames, numberValues
}

func resourceListLimit(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 100, nil
	}
	limit, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || limit < 1 || limit > 500 {
		return 0, errors.New("limit must be an integer between 1 and 500")
	}
	return limit, nil
}

func decodeResourceJSON(c *gin.Context, target any) bool {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeResourceRequestError(c, err)
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("request body must contain one JSON value")
		}
		writeResourceRequestError(c, err)
		return false
	}
	return true
}

func decodeOptionalResourceJSON(c *gin.Context, target any) bool {
	if c.Request.Body == nil || c.Request.ContentLength == 0 {
		return true
	}
	return decodeResourceJSON(c, target)
}

func writeResourceRequestError(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, gin.H{
		"status": "error", "code": "invalid_request", "message": err.Error(), "request_id": requestID(c),
	})
}

func writeResourceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, resourcecatalog.ErrNotFound), errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "code": "resource_not_found", "message": "资源不存在", "request_id": requestID(c)})
	case isResourceConflict(err):
		c.JSON(http.StatusConflict, gin.H{"status": "error", "code": "resource_conflict", "message": "资源已存在或正在使用", "request_id": requestID(c)})
	case isResourceValidationError(err):
		writeResourceRequestError(c, err)
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "code": "resource_error", "message": "资源请求失败", "request_id": requestID(c)})
	}
}

func isResourceConflict(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") || strings.Contains(message, "duplicate key")
}

func isResourceValidationError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "resource catalog:") ||
		strings.Contains(message, "stable card container identity required") ||
		strings.Contains(message, "subscription profile has no")
}

func jsonString(value any) string {
	result, _ := value.(string)
	return result
}

func jsonInt(value any) int {
	switch typed := value.(type) {
	case json.Number:
		result, _ := strconv.Atoi(typed.String())
		return result
	case float64:
		return int(typed)
	case int:
		return typed
	default:
		return 0
	}
}
