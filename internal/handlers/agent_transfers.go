package handlers

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/websocket"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// CreateAgentTransferRequest represents the request to create an agent transfer
type CreateAgentTransferRequest struct {
	ContactID       string  `json:"contact_id"`
	WhatsAppAccount string  `json:"whatsapp_account"`
	AgentID         *string `json:"agent_id"`
	Notes           string  `json:"notes"`
	Source          string  `json:"source"` // manual, flow, keyword
}

// AssignTransferRequest represents the request to assign a transfer to an agent
type AssignTransferRequest struct {
	AgentID *string `json:"agent_id"`
}

// AgentTransferResponse represents an agent transfer in API responses
type AgentTransferResponse struct {
	ID                string  `json:"id"`
	ContactID         string  `json:"contact_id"`
	ContactName       string  `json:"contact_name"`
	PhoneNumber       string  `json:"phone_number"`
	WhatsAppAccount   string  `json:"whatsapp_account"`
	Status            string  `json:"status"`
	Source            string  `json:"source"`
	AgentID           *string `json:"agent_id,omitempty"`
	AgentName         *string `json:"agent_name,omitempty"`
	TransferredBy     *string `json:"transferred_by,omitempty"`
	TransferredByName *string `json:"transferred_by_name,omitempty"`
	Notes             string  `json:"notes"`
	TransferredAt     string  `json:"transferred_at"`
	ResumedAt         *string `json:"resumed_at,omitempty"`
	ResumedBy         *string `json:"resumed_by,omitempty"`
	ResumedByName     *string `json:"resumed_by_name,omitempty"`
}

// ListAgentTransfers lists agent transfers for the organization
// Agents see only their assigned transfers; Admin/Manager see all
func (a *App) ListAgentTransfers(r *fastglue.Request) error {
	orgID, err := a.getOrgIDFromContext(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}

	userID, _ := r.RequestCtx.UserValue("user_id").(uuid.UUID)
	role, _ := r.RequestCtx.UserValue("role").(string)

	// Query params
	status := string(r.RequestCtx.QueryArgs().Peek("status"))

	query := a.DB.Where("organization_id = ?", orgID).
		Preload("Contact").
		Preload("Agent").
		Preload("TransferredByUser").
		Preload("ResumedByUser").
		Order("transferred_at ASC") // FIFO

	// Filter by status if provided
	if status != "" {
		query = query.Where("status = ?", status)
	}

	// Agents only see their own transfers
	if role == "agent" {
		query = query.Where("agent_id = ?", userID)
	}

	var transfers []models.AgentTransfer
	if err := query.Find(&transfers).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to fetch transfers", nil, "")
	}

	// Get queue count for agents
	var queueCount int64
	a.DB.Model(&models.AgentTransfer{}).
		Where("organization_id = ? AND status = ? AND agent_id IS NULL", orgID, "active").
		Count(&queueCount)

	a.Log.Info("ListAgentTransfers", "org_id", orgID, "role", role, "user_id", userID, "transfers_count", len(transfers), "queue_count", queueCount)

	// Build response
	response := make([]AgentTransferResponse, len(transfers))
	for i, t := range transfers {
		resp := AgentTransferResponse{
			ID:              t.ID.String(),
			ContactID:       t.ContactID.String(),
			PhoneNumber:     t.PhoneNumber,
			WhatsAppAccount: t.WhatsAppAccount,
			Status:          t.Status,
			Source:          t.Source,
			Notes:           t.Notes,
			TransferredAt:   t.TransferredAt.Format(time.RFC3339),
		}

		if t.Contact != nil {
			resp.ContactName = t.Contact.ProfileName
		}

		if t.AgentID != nil {
			agentIDStr := t.AgentID.String()
			resp.AgentID = &agentIDStr
			if t.Agent != nil {
				resp.AgentName = &t.Agent.FullName
			}
		}

		if t.TransferredByUserID != nil {
			transferredBy := t.TransferredByUserID.String()
			resp.TransferredBy = &transferredBy
			if t.TransferredByUser != nil {
				resp.TransferredByName = &t.TransferredByUser.FullName
			}
		}

		if t.ResumedAt != nil {
			resumedAt := t.ResumedAt.Format(time.RFC3339)
			resp.ResumedAt = &resumedAt
		}

		if t.ResumedBy != nil {
			resumedBy := t.ResumedBy.String()
			resp.ResumedBy = &resumedBy
			if t.ResumedByUser != nil {
				resp.ResumedByName = &t.ResumedByUser.FullName
			}
		}

		response[i] = resp
	}

	return r.SendEnvelope(map[string]any{
		"transfers":   response,
		"queue_count": queueCount,
	})
}

// CreateAgentTransfer creates a new agent transfer
func (a *App) CreateAgentTransfer(r *fastglue.Request) error {
	orgID, err := a.getOrgIDFromContext(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}

	userID, _ := r.RequestCtx.UserValue("user_id").(uuid.UUID)

	var req CreateAgentTransferRequest
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}

	if req.ContactID == "" {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "contact_id is required", nil, "")
	}

	contactID, err := uuid.Parse(req.ContactID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid contact_id", nil, "")
	}

	// Get contact
	var contact models.Contact
	if err := a.DB.Where("id = ? AND organization_id = ?", contactID, orgID).First(&contact).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Contact not found", nil, "")
	}

	// Check for existing active transfer
	var existingCount int64
	a.DB.Model(&models.AgentTransfer{}).
		Where("organization_id = ? AND contact_id = ? AND status = ?", orgID, contactID, "active").
		Count(&existingCount)

	if existingCount > 0 {
		return r.SendErrorEnvelope(fasthttp.StatusConflict, "Contact already has an active transfer", nil, "")
	}

	// Get chatbot settings to check AssignToSameAgent
	var settings models.ChatbotSettings
	a.DB.Where("organization_id = ? AND whats_app_account = ?", orgID, req.WhatsAppAccount).
		Or("organization_id = ? AND whats_app_account = ''", orgID).
		Order("whats_app_account DESC"). // Prefer account-specific settings
		First(&settings)

	// Determine agent assignment
	var agentID *uuid.UUID

	// First, try explicit agent from request
	if req.AgentID != nil && *req.AgentID != "" {
		parsedAgentID, err := uuid.Parse(*req.AgentID)
		if err != nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid agent_id", nil, "")
		}
		// Verify agent exists and is available
		var agent models.User
		if err := a.DB.Where("id = ? AND organization_id = ?", parsedAgentID, orgID).First(&agent).Error; err != nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Agent not found", nil, "")
		}
		if !agent.IsAvailable {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Agent is currently away", nil, "")
		}
		agentID = &parsedAgentID
	} else if settings.AssignToSameAgent && contact.AssignedUserID != nil {
		// Auto-assign to contact's existing assigned agent (if setting enabled and agent is available)
		var assignedAgent models.User
		if a.DB.Where("id = ?", contact.AssignedUserID).First(&assignedAgent).Error == nil && assignedAgent.IsAvailable {
			agentID = contact.AssignedUserID
		}
		// If agent is not available, falls through to queue (agentID remains nil)
	}
	// Otherwise, agentID remains nil (goes to queue)

	// Determine source
	source := req.Source
	if source == "" {
		source = "manual"
	}

	// Create transfer
	transfer := models.AgentTransfer{
		BaseModel:           models.BaseModel{ID: uuid.New()},
		OrganizationID:      orgID,
		ContactID:           contactID,
		WhatsAppAccount:     req.WhatsAppAccount,
		PhoneNumber:         contact.PhoneNumber,
		Status:              "active",
		Source:              source,
		AgentID:             agentID,
		TransferredByUserID: &userID,
		Notes:               req.Notes,
		TransferredAt:       time.Now(),
	}

	if err := a.DB.Create(&transfer).Error; err != nil {
		a.Log.Error("Failed to create agent transfer", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to create transfer", nil, "")
	}

	// Update contact assignment if agent assigned
	if agentID != nil {
		a.DB.Model(&contact).Update("assigned_user_id", agentID)
	}

	// End any active chatbot session
	a.DB.Model(&models.ChatbotSession{}).
		Where("organization_id = ? AND contact_id = ? AND status = ?", orgID, contactID, "active").
		Updates(map[string]any{
			"status":       "cancelled",
			"completed_at": time.Now(),
		})

	// Broadcast WebSocket notification
	a.broadcastTransferCreated(&transfer, &contact)

	// Dispatch webhook for transfer created
	var agentIDStr *string
	var agentName *string
	if transfer.AgentID != nil {
		idStr := transfer.AgentID.String()
		agentIDStr = &idStr
	}
	a.DispatchWebhook(orgID, EventTransferCreated, TransferEventData{
		TransferID:      transfer.ID.String(),
		ContactID:       contact.ID.String(),
		ContactPhone:    contact.PhoneNumber,
		ContactName:     contact.ProfileName,
		Source:          transfer.Source,
		Reason:          transfer.Notes,
		AgentID:         agentIDStr,
		AgentName:       agentName,
		WhatsAppAccount: transfer.WhatsAppAccount,
	})

	// Load relations for response
	a.DB.Preload("Agent").Preload("TransferredByUser").First(&transfer, transfer.ID)

	resp := AgentTransferResponse{
		ID:              transfer.ID.String(),
		ContactID:       transfer.ContactID.String(),
		ContactName:     contact.ProfileName,
		PhoneNumber:     transfer.PhoneNumber,
		WhatsAppAccount: transfer.WhatsAppAccount,
		Status:          transfer.Status,
		Source:          transfer.Source,
		Notes:           transfer.Notes,
		TransferredAt:   transfer.TransferredAt.Format(time.RFC3339),
	}

	if transfer.AgentID != nil {
		agentIDStr := transfer.AgentID.String()
		resp.AgentID = &agentIDStr
		if transfer.Agent != nil {
			resp.AgentName = &transfer.Agent.FullName
		}
	}

	if transfer.TransferredByUserID != nil {
		transferredBy := transfer.TransferredByUserID.String()
		resp.TransferredBy = &transferredBy
		if transfer.TransferredByUser != nil {
			resp.TransferredByName = &transfer.TransferredByUser.FullName
		}
	}

	return r.SendEnvelope(map[string]any{
		"transfer": resp,
		"message":  "Transfer created successfully",
	})
}

// ResumeFromTransfer resumes chatbot processing for a transferred contact
func (a *App) ResumeFromTransfer(r *fastglue.Request) error {
	orgID, err := a.getOrgIDFromContext(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}

	userID, _ := r.RequestCtx.UserValue("user_id").(uuid.UUID)

	transferIDStr := r.RequestCtx.UserValue("id").(string)
	transferID, err := uuid.Parse(transferIDStr)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid transfer ID", nil, "")
	}

	var transfer models.AgentTransfer
	if err := a.DB.Where("id = ? AND organization_id = ?", transferID, orgID).First(&transfer).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Transfer not found", nil, "")
	}

	if transfer.Status != "active" {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Transfer is not active", nil, "")
	}

	// Update transfer
	now := time.Now()
	transfer.Status = "resumed"
	transfer.ResumedAt = &now
	transfer.ResumedBy = &userID

	if err := a.DB.Save(&transfer).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to resume transfer", nil, "")
	}

	// Get chatbot settings to check AssignToSameAgent
	var settings models.ChatbotSettings
	a.DB.Where("organization_id = ? AND whats_app_account = ?", orgID, transfer.WhatsAppAccount).
		Or("organization_id = ? AND whats_app_account = ''", orgID).
		Order("whats_app_account DESC").
		First(&settings)

	// If AssignToSameAgent is disabled, unassign the contact
	if !settings.AssignToSameAgent {
		a.DB.Model(&models.Contact{}).
			Where("id = ?", transfer.ContactID).
			Update("assigned_user_id", nil)
	}

	// Broadcast WebSocket notification
	a.broadcastTransferResumed(&transfer)

	// Get contact for webhook data
	var contact models.Contact
	a.DB.Where("id = ?", transfer.ContactID).First(&contact)

	// Dispatch webhook for transfer resumed
	a.DispatchWebhook(orgID, EventTransferResumed, TransferEventData{
		TransferID:      transfer.ID.String(),
		ContactID:       contact.ID.String(),
		ContactPhone:    contact.PhoneNumber,
		ContactName:     contact.ProfileName,
		Source:          transfer.Source,
		WhatsAppAccount: transfer.WhatsAppAccount,
	})

	return r.SendEnvelope(map[string]any{
		"message": "Transfer resumed, chatbot is now active for this contact",
	})
}

// AssignAgentTransfer assigns a transfer to a specific agent
func (a *App) AssignAgentTransfer(r *fastglue.Request) error {
	orgID, err := a.getOrgIDFromContext(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}

	role, _ := r.RequestCtx.UserValue("role").(string)
	userID, _ := r.RequestCtx.UserValue("user_id").(uuid.UUID)

	transferIDStr := r.RequestCtx.UserValue("id").(string)
	transferID, err := uuid.Parse(transferIDStr)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid transfer ID", nil, "")
	}

	var req AssignTransferRequest
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}

	var transfer models.AgentTransfer
	if err := a.DB.Where("id = ? AND organization_id = ?", transferID, orgID).
		Preload("Contact").First(&transfer).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Transfer not found", nil, "")
	}

	if transfer.Status != "active" {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Transfer is not active", nil, "")
	}

	// Determine target agent
	var targetAgentID *uuid.UUID

	if req.AgentID != nil && *req.AgentID != "" {
		// Explicit assignment
		if role == "agent" {
			return r.SendErrorEnvelope(fasthttp.StatusForbidden, "Agents cannot assign transfers to others", nil, "")
		}

		parsedAgentID, err := uuid.Parse(*req.AgentID)
		if err != nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid agent_id", nil, "")
		}

		// Verify agent exists and is available
		var agent models.User
		if err := a.DB.Where("id = ? AND organization_id = ?", parsedAgentID, orgID).First(&agent).Error; err != nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Agent not found", nil, "")
		}
		if !agent.IsAvailable {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Agent is currently away", nil, "")
		}
		targetAgentID = &parsedAgentID
	} else if req.AgentID == nil && role == "agent" {
		// Agent self-assigning (null means "assign to me")
		targetAgentID = &userID
	}

	// Update transfer
	transfer.AgentID = targetAgentID
	if err := a.DB.Save(&transfer).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to assign transfer", nil, "")
	}

	// Update contact assignment
	if targetAgentID != nil && transfer.Contact != nil {
		a.DB.Model(transfer.Contact).Update("assigned_user_id", targetAgentID)
	}

	// Broadcast WebSocket notification
	a.broadcastTransferAssigned(&transfer)

	// Dispatch webhook for transfer assigned
	var agentIDStr *string
	var agentName *string
	if targetAgentID != nil {
		idStr := targetAgentID.String()
		agentIDStr = &idStr
		// Get agent name
		var agent models.User
		if a.DB.Where("id = ?", targetAgentID).First(&agent).Error == nil {
			agentName = &agent.FullName
		}
	}
	contactPhone := ""
	contactName := ""
	if transfer.Contact != nil {
		contactPhone = transfer.Contact.PhoneNumber
		contactName = transfer.Contact.ProfileName
	}
	a.DispatchWebhook(orgID, EventTransferAssigned, TransferEventData{
		TransferID:      transfer.ID.String(),
		ContactID:       transfer.ContactID.String(),
		ContactPhone:    contactPhone,
		ContactName:     contactName,
		Source:          transfer.Source,
		AgentID:         agentIDStr,
		AgentName:       agentName,
		WhatsAppAccount: transfer.WhatsAppAccount,
	})

	return r.SendEnvelope(map[string]any{
		"message":  "Transfer assigned successfully",
		"agent_id": targetAgentID,
	})
}

// PickNextTransfer allows an agent to pick the next unassigned transfer from the queue
func (a *App) PickNextTransfer(r *fastglue.Request) error {
	orgID, err := a.getOrgIDFromContext(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}

	userID, _ := r.RequestCtx.UserValue("user_id").(uuid.UUID)
	role, _ := r.RequestCtx.UserValue("role").(string)

	// Check if agent queue pickup is allowed
	var settings models.ChatbotSettings
	a.DB.Where("organization_id = ? AND whats_app_account = ?", orgID, "").First(&settings)

	if role == "agent" && !settings.AllowAgentQueuePickup {
		return r.SendErrorEnvelope(fasthttp.StatusForbidden, "Queue pickup is not allowed", nil, "")
	}

	// Find oldest unassigned active transfer (FIFO)
	var transfer models.AgentTransfer
	result := a.DB.Where("organization_id = ? AND status = ? AND agent_id IS NULL", orgID, "active").
		Preload("Contact").
		Order("transferred_at ASC").
		First(&transfer)

	if result.Error != nil {
		return r.SendEnvelope(map[string]any{
			"message":  "No transfers in queue",
			"transfer": nil,
		})
	}

	// Assign to current user (self-pick)
	transfer.AgentID = &userID
	// If no one initiated the transfer, mark the picker as the one who initiated (self-pick)
	if transfer.TransferredByUserID == nil {
		transfer.TransferredByUserID = &userID
	}
	if err := a.DB.Save(&transfer).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to pick transfer", nil, "")
	}

	// Update contact assignment
	if transfer.Contact != nil {
		a.DB.Model(transfer.Contact).Update("assigned_user_id", userID)
	}

	// Load agent info
	var agent models.User
	a.DB.First(&agent, userID)

	// Broadcast WebSocket notification
	a.broadcastTransferAssigned(&transfer)

	resp := AgentTransferResponse{
		ID:              transfer.ID.String(),
		ContactID:       transfer.ContactID.String(),
		PhoneNumber:     transfer.PhoneNumber,
		WhatsAppAccount: transfer.WhatsAppAccount,
		Status:          transfer.Status,
		Source:          transfer.Source,
		Notes:           transfer.Notes,
		TransferredAt:   transfer.TransferredAt.Format(time.RFC3339),
	}

	if transfer.Contact != nil {
		resp.ContactName = transfer.Contact.ProfileName
	}

	agentIDStr := userID.String()
	resp.AgentID = &agentIDStr
	resp.AgentName = &agent.FullName

	// Set TransferredBy (self-pick)
	if transfer.TransferredByUserID != nil {
		transferredBy := transfer.TransferredByUserID.String()
		resp.TransferredBy = &transferredBy
		resp.TransferredByName = &agent.FullName
	}

	return r.SendEnvelope(map[string]any{
		"message":  "Transfer picked successfully",
		"transfer": resp,
	})
}

// hasActiveAgentTransfer checks if a contact has an active agent transfer
func (a *App) hasActiveAgentTransfer(orgID, contactID uuid.UUID) bool {
	var count int64
	a.DB.Model(&models.AgentTransfer{}).
		Where("organization_id = ? AND contact_id = ? AND status = ?", orgID, contactID, "active").
		Count(&count)
	return count > 0
}

// WebSocket broadcast helpers

func (a *App) broadcastTransferCreated(transfer *models.AgentTransfer, contact *models.Contact) {
	if a.WSHub == nil {
		return
	}

	payload := map[string]any{
		"id":               transfer.ID.String(),
		"contact_id":       transfer.ContactID.String(),
		"contact_name":     contact.ProfileName,
		"phone_number":     transfer.PhoneNumber,
		"whatsapp_account": transfer.WhatsAppAccount,
		"status":           transfer.Status,
		"source":           transfer.Source,
		"notes":            transfer.Notes,
		"transferred_at":   transfer.TransferredAt.Format(time.RFC3339),
	}

	if transfer.AgentID != nil {
		payload["agent_id"] = transfer.AgentID.String()
	}

	a.WSHub.BroadcastToOrg(transfer.OrganizationID, websocket.WSMessage{
		Type:    websocket.TypeAgentTransfer,
		Payload: payload,
	})
}

func (a *App) broadcastTransferResumed(transfer *models.AgentTransfer) {
	if a.WSHub == nil {
		return
	}

	payload := map[string]any{
		"id":         transfer.ID.String(),
		"contact_id": transfer.ContactID.String(),
		"status":     transfer.Status,
	}

	if transfer.ResumedAt != nil {
		payload["resumed_at"] = transfer.ResumedAt.Format(time.RFC3339)
	}
	if transfer.ResumedBy != nil {
		payload["resumed_by"] = transfer.ResumedBy.String()
	}

	a.WSHub.BroadcastToOrg(transfer.OrganizationID, websocket.WSMessage{
		Type:    websocket.TypeAgentTransferResume,
		Payload: payload,
	})
}

func (a *App) broadcastTransferAssigned(transfer *models.AgentTransfer) {
	if a.WSHub == nil {
		return
	}

	payload := map[string]any{
		"id":         transfer.ID.String(),
		"contact_id": transfer.ContactID.String(),
		"status":     transfer.Status,
	}

	if transfer.AgentID != nil {
		payload["agent_id"] = transfer.AgentID.String()
	}

	a.WSHub.BroadcastToOrg(transfer.OrganizationID, websocket.WSMessage{
		Type:    websocket.TypeAgentTransferAssign,
		Payload: payload,
	})
}

// createTransferToQueue creates an unassigned agent transfer that goes to the queue
func (a *App) createTransferToQueue(account *models.WhatsAppAccount, contact *models.Contact, source string) {
	// Check for existing active transfer
	var existingCount int64
	a.DB.Model(&models.AgentTransfer{}).
		Where("organization_id = ? AND contact_id = ? AND status = ?", account.OrganizationID, contact.ID, "active").
		Count(&existingCount)

	if existingCount > 0 {
		a.Log.Debug("Contact already has active transfer, skipping", "contact_id", contact.ID, "source", source)
		return
	}

	// Create unassigned transfer (goes to queue)
	transfer := models.AgentTransfer{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  account.OrganizationID,
		ContactID:       contact.ID,
		WhatsAppAccount: account.Name,
		PhoneNumber:     contact.PhoneNumber,
		Status:          "active",
		Source:          source,
		AgentID:         nil, // Unassigned - goes to queue
		TransferredAt:   time.Now(),
	}

	if err := a.DB.Create(&transfer).Error; err != nil {
		a.Log.Error("Failed to create transfer to queue", "error", err, "contact_id", contact.ID, "source", source)
		return
	}

	a.Log.Info("Transfer created to agent queue", "transfer_id", transfer.ID, "contact_id", contact.ID, "source", source)

	// Broadcast to WebSocket
	a.broadcastTransferCreated(&transfer, contact)
}

// createTransferFromKeyword creates an agent transfer triggered by a keyword rule
func (a *App) createTransferFromKeyword(account *models.WhatsAppAccount, contact *models.Contact) {
	// Check for existing active transfer
	var existingCount int64
	a.DB.Model(&models.AgentTransfer{}).
		Where("organization_id = ? AND contact_id = ? AND status = ?", account.OrganizationID, contact.ID, "active").
		Count(&existingCount)

	if existingCount > 0 {
		a.Log.Info("Contact already has active transfer, skipping keyword transfer", "contact_id", contact.ID)
		return
	}

	// Get chatbot settings to check AssignToSameAgent and business hours
	var settings models.ChatbotSettings
	a.DB.Where("organization_id = ? AND whats_app_account = ?", account.OrganizationID, account.Name).
		Or("organization_id = ? AND whats_app_account = ''", account.OrganizationID).
		Order("whats_app_account DESC"). // Prefer account-specific settings
		First(&settings)

	// Check business hours - if outside hours, send out of hours message instead of transfer
	if settings.BusinessHoursEnabled && len(settings.BusinessHours) > 0 {
		if !a.isWithinBusinessHours(settings.BusinessHours) {
			a.Log.Info("Outside business hours, sending out of hours message instead of transfer", "contact_id", contact.ID)
			if settings.OutOfHoursMessage != "" {
				a.sendAndSaveTextMessage(account, contact, settings.OutOfHoursMessage)
			}
			return
		}
	}

	// Determine agent assignment
	var agentID *uuid.UUID
	if settings.AssignToSameAgent && contact.AssignedUserID != nil {
		// Check if the assigned agent is available
		var assignedAgent models.User
		if a.DB.Where("id = ?", contact.AssignedUserID).First(&assignedAgent).Error == nil && assignedAgent.IsAvailable {
			agentID = contact.AssignedUserID
		}
		// If agent is not available, falls through to queue (agentID remains nil)
	}

	// Create transfer
	transfer := models.AgentTransfer{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  account.OrganizationID,
		ContactID:       contact.ID,
		WhatsAppAccount: account.Name,
		PhoneNumber:     contact.PhoneNumber,
		Status:          "active",
		Source:          "keyword",
		AgentID:         agentID,
		TransferredAt:   time.Now(),
	}

	if err := a.DB.Create(&transfer).Error; err != nil {
		a.Log.Error("Failed to create keyword-triggered transfer", "error", err, "contact_id", contact.ID)
		return
	}

	// Update contact assignment if agent assigned
	if agentID != nil {
		a.DB.Model(&contact).Update("assigned_user_id", agentID)
	}

	// End any active chatbot session
	a.DB.Model(&models.ChatbotSession{}).
		Where("organization_id = ? AND contact_id = ? AND status = ?", account.OrganizationID, contact.ID, "active").
		Updates(map[string]any{
			"status":       "cancelled",
			"completed_at": time.Now(),
		})

	var agentIDStr string
	if agentID != nil {
		agentIDStr = agentID.String()
	}
	a.Log.Info("Agent transfer created from keyword rule",
		"transfer_id", transfer.ID,
		"contact_id", contact.ID,
		"agent_id", agentIDStr,
	)

	// Broadcast to WebSocket
	a.broadcastTransferCreated(&transfer, contact)
}
