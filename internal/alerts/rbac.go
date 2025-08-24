package alerts

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Rajchodisetti/trading-app/internal/observ"
)

// RBACManager handles role-based access control for Slack commands
type RBACManager struct {
	signingSecret string
	permissions   map[string][]string // userID -> permissions
	auditLog      *AuditLogger
}

// Permission constants
const (
	PermissionViewPortfolio     = "view_portfolio"
	PermissionViewRisk         = "view_risk"
	PermissionManualHalt       = "manual_halt"
	PermissionInitiateRecovery = "initiate_recovery" 
	PermissionEmergencyHalt    = "emergency_halt"
	PermissionConfigChange     = "config_change"
	PermissionAuditAccess      = "audit_access"
)

// AuditEntry represents an audit log entry
type AuditEntry struct {
	Timestamp    time.Time              `json:"timestamp"`
	UserID       string                 `json:"user_id"`
	UserName     string                 `json:"user_name,omitempty"`
	Action       string                 `json:"action"`
	Resource     string                 `json:"resource"`
	Outcome      string                 `json:"outcome"` // success, denied, error
	Details      map[string]interface{} `json:"details,omitempty"`
	IPAddress    string                 `json:"ip_address,omitempty"`
	CorrelationID string                `json:"correlation_id,omitempty"`
}

// AuditLogger handles audit trail logging
type AuditLogger struct {
	logPath string
}

// SlackRequest represents an incoming Slack request
type SlackRequest struct {
	UserID        string
	UserName      string
	Command       string
	Text          string
	ChannelID     string
	TeamID        string
	Timestamp     time.Time
	Signature     string
	Body          string
	CorrelationID string
}

// NewRBACManager creates a new RBAC manager
func NewRBACManager(signingSecret string, auditLogPath string) *RBACManager {
	return &RBACManager{
		signingSecret: signingSecret,
		permissions:   loadPermissions(),
		auditLog:      &AuditLogger{logPath: auditLogPath},
	}
}

// ValidateRequest validates a Slack request signature and timestamp
func (rbac *RBACManager) ValidateRequest(signature, timestamp, body string) error {
	// Check timestamp (prevent replay attacks)
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid timestamp: %w", err)
	}
	
	// Reject requests older than 5 minutes
	if time.Now().Unix()-ts > 300 {
		return fmt.Errorf("request too old")
	}
	
	// Verify signature
	baseString := fmt.Sprintf("v0:%s:%s", timestamp, body)
	hash := hmac.New(sha256.New, []byte(rbac.signingSecret))
	hash.Write([]byte(baseString))
	expectedSig := "v0=" + hex.EncodeToString(hash.Sum(nil))
	
	if !hmac.Equal([]byte(signature), []byte(expectedSig)) {
		rbac.auditLog.LogSecurityEvent("invalid_signature", map[string]interface{}{
			"provided_signature": signature,
			"expected_signature": expectedSig,
			"timestamp":          timestamp,
		})
		return fmt.Errorf("invalid signature")
	}
	
	return nil
}

// AuthorizeAction checks if a user has permission to perform an action
func (rbac *RBACManager) AuthorizeAction(userID, action string, correlationID string) error {
	userPerms, exists := rbac.permissions[userID]
	if !exists {
		rbac.auditLog.LogAuditEvent(AuditEntry{
			Timestamp:     time.Now(),
			UserID:        userID,
			Action:        action,
			Resource:      "rbac_authorization",
			Outcome:       "denied",
			Details:       map[string]interface{}{"reason": "user_not_found"},
			CorrelationID: correlationID,
		})
		return fmt.Errorf("user %s not found in permissions", userID)
	}
	
	// Check if user has required permission
	hasPermission := false
	for _, perm := range userPerms {
		if perm == action || perm == "*" { // * is wildcard permission
			hasPermission = true
			break
		}
	}
	
	if !hasPermission {
		rbac.auditLog.LogAuditEvent(AuditEntry{
			Timestamp:     time.Now(),
			UserID:        userID,
			Action:        action,
			Resource:      "rbac_authorization",
			Outcome:       "denied",
			Details:       map[string]interface{}{"reason": "insufficient_permissions", "user_permissions": userPerms},
			CorrelationID: correlationID,
		})
		return fmt.Errorf("user %s lacks permission %s", userID, action)
	}
	
	// Log successful authorization
	rbac.auditLog.LogAuditEvent(AuditEntry{
		Timestamp:     time.Now(),
		UserID:        userID,
		Action:        action,
		Resource:      "rbac_authorization",
		Outcome:       "success",
		Details:       map[string]interface{}{"granted_permission": action},
		CorrelationID: correlationID,
	})
	
	observ.IncCounter("rbac_authorizations_total", map[string]string{
		"user_id": userID,
		"action":  action,
		"outcome": "success",
	})
	
	return nil
}

// RequireTwoPersonApproval checks if an action requires two-person approval
func (rbac *RBACManager) RequireTwoPersonApproval(action string) bool {
	highRiskActions := []string{
		PermissionInitiateRecovery,
		PermissionEmergencyHalt,
		PermissionConfigChange,
	}
	
	for _, highRisk := range highRiskActions {
		if action == highRisk {
			return true
		}
	}
	
	return false
}

// ValidateTwoPersonApproval validates that two authorized users have approved an action
func (rbac *RBACManager) ValidateTwoPersonApproval(action string, userIDs []string, correlationID string) error {
	if len(userIDs) < 2 {
		return fmt.Errorf("two-person approval required, only %d approver(s) provided", len(userIDs))
	}
	
	// Verify each approver has permission
	approvedUsers := make([]string, 0)
	for _, userID := range userIDs {
		if err := rbac.AuthorizeAction(userID, action, correlationID); err == nil {
			approvedUsers = append(approvedUsers, userID)
		}
	}
	
	if len(approvedUsers) < 2 {
		rbac.auditLog.LogAuditEvent(AuditEntry{
			Timestamp:     time.Now(),
			UserID:        strings.Join(userIDs, ","),
			Action:        action,
			Resource:      "two_person_approval",
			Outcome:       "denied",
			Details:       map[string]interface{}{"reason": "insufficient_authorized_approvers", "approved_count": len(approvedUsers)},
			CorrelationID: correlationID,
		})
		return fmt.Errorf("insufficient authorized approvers: %d of 2 required", len(approvedUsers))
	}
	
	// Log successful two-person approval
	rbac.auditLog.LogAuditEvent(AuditEntry{
		Timestamp:     time.Now(),
		UserID:        strings.Join(approvedUsers, ","),
		Action:        action,
		Resource:      "two_person_approval",
		Outcome:       "success",
		Details:       map[string]interface{}{"approved_users": approvedUsers},
		CorrelationID: correlationID,
	})
	
	observ.IncCounter("two_person_approvals_total", map[string]string{
		"action":  action,
		"outcome": "success",
	})
	
	return nil
}

// GetUserPermissions returns the permissions for a user
func (rbac *RBACManager) GetUserPermissions(userID string) []string {
	return rbac.permissions[userID]
}

// AddUserPermission adds a permission to a user (for dynamic permission management)
func (rbac *RBACManager) AddUserPermission(adminUserID, targetUserID, permission string, correlationID string) error {
	// Check if admin has permission to modify permissions
	if err := rbac.AuthorizeAction(adminUserID, PermissionConfigChange, correlationID); err != nil {
		return fmt.Errorf("unauthorized to modify permissions: %w", err)
	}
	
	if rbac.permissions[targetUserID] == nil {
		rbac.permissions[targetUserID] = make([]string, 0)
	}
	
	// Check if permission already exists
	for _, existingPerm := range rbac.permissions[targetUserID] {
		if existingPerm == permission {
			return nil // Already has permission
		}
	}
	
	rbac.permissions[targetUserID] = append(rbac.permissions[targetUserID], permission)
	
	// Log permission change
	rbac.auditLog.LogAuditEvent(AuditEntry{
		Timestamp:     time.Now(),
		UserID:        adminUserID,
		Action:        "add_user_permission",
		Resource:      "rbac_permissions",
		Outcome:       "success",
		Details: map[string]interface{}{
			"target_user":  targetUserID,
			"permission":   permission,
			"admin_action": true,
		},
		CorrelationID: correlationID,
	})
	
	return nil
}

// LogAuditEvent logs an audit event for compliance and monitoring
func (al *AuditLogger) LogAuditEvent(entry AuditEntry) {
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	
	// Create directory if it doesn't exist
	if err := os.MkdirAll("data/audit", 0755); err != nil {
		observ.IncCounter("audit_log_errors_total", map[string]string{"error": "mkdir"})
		return
	}
	
	// Open audit log file (create if doesn't exist)
	file, err := os.OpenFile(al.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		observ.IncCounter("audit_log_errors_total", map[string]string{"error": "open_file"})
		return
	}
	defer file.Close()
	
	// Serialize as JSON
	entryJSON, err := json.Marshal(entry)
	if err != nil {
		observ.IncCounter("audit_log_errors_total", map[string]string{"error": "marshal"})
		return
	}
	
	// Write to file
	if _, err := fmt.Fprintf(file, "%s\n", entryJSON); err != nil {
		observ.IncCounter("audit_log_errors_total", map[string]string{"error": "write"})
		return
	}
	
	// Update metrics
	observ.IncCounter("audit_entries_total", map[string]string{
		"user_id":  entry.UserID,
		"action":   entry.Action,
		"outcome":  entry.Outcome,
		"resource": entry.Resource,
	})
}

// LogSecurityEvent logs a security-related event
func (al *AuditLogger) LogSecurityEvent(eventType string, details map[string]interface{}) {
	entry := AuditEntry{
		Timestamp: time.Now(),
		Action:    "security_event",
		Resource:  "security",
		Outcome:   eventType,
		Details:   details,
	}
	
	al.LogAuditEvent(entry)
	
	// Increment security metrics
	observ.IncCounter("security_events_total", map[string]string{
		"event_type": eventType,
	})
}

// GetAuditHistory retrieves recent audit entries for compliance reporting
func (al *AuditLogger) GetAuditHistory(maxEntries int, filter map[string]string) ([]AuditEntry, error) {
	// This is a simplified implementation - in production, you'd want
	// to use a proper database or log aggregation system
	
	file, err := os.Open(al.logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []AuditEntry{}, nil
		}
		return nil, fmt.Errorf("failed to open audit log: %w", err)
	}
	defer file.Close()
	
	// For now, return empty slice - full implementation would parse the file
	// and apply filters
	return []AuditEntry{}, nil
}

// loadPermissions loads user permissions from environment variables or config file
func loadPermissions() map[string][]string {
	permissions := make(map[string][]string)
	
	// Load from environment variables (format: USER_ID:permission1,permission2)
	permEnv := os.Getenv("SLACK_USER_PERMISSIONS")
	if permEnv != "" {
		userPerms := strings.Split(permEnv, ";")
		for _, userPerm := range userPerms {
			parts := strings.Split(userPerm, ":")
			if len(parts) == 2 {
				userID := parts[0]
				perms := strings.Split(parts[1], ",")
				permissions[userID] = perms
			}
		}
	}
	
	// Default permissions for development (would be removed in production)
	if len(permissions) == 0 {
		permissions["U12345"] = []string{
			PermissionViewPortfolio,
			PermissionViewRisk,
			PermissionManualHalt,
			PermissionInitiateRecovery,
		}
		permissions["U67890"] = []string{
			PermissionViewPortfolio,
			PermissionViewRisk,
			PermissionEmergencyHalt,
			PermissionConfigChange,
		}
		// Admin user with all permissions
		permissions["UADMIN"] = []string{"*"}
	}
	
	return permissions
}

// SlackCommandHandler handles authenticated and authorized Slack commands
type SlackCommandHandler struct {
	rbac *RBACManager
}

// NewSlackCommandHandler creates a new command handler with RBAC
func NewSlackCommandHandler(signingSecret, auditLogPath string) *SlackCommandHandler {
	return &SlackCommandHandler{
		rbac: NewRBACManager(signingSecret, auditLogPath),
	}
}

// HandleCommand processes an incoming Slack command with full authorization
func (sch *SlackCommandHandler) HandleCommand(req SlackRequest) (interface{}, error) {
	// Validate request signature
	if err := sch.rbac.ValidateRequest(req.Signature, strconv.FormatInt(req.Timestamp.Unix(), 10), req.Body); err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	
	// Determine required permission based on command
	permission := sch.getRequiredPermission(req.Command, req.Text)
	
	// Authorize user
	if err := sch.rbac.AuthorizeAction(req.UserID, permission, req.CorrelationID); err != nil {
		return sch.createUnauthorizedResponse(req.Command, err), nil
	}
	
	// Log successful command execution
	sch.rbac.auditLog.LogAuditEvent(AuditEntry{
		Timestamp:     time.Now(),
		UserID:        req.UserID,
		UserName:      req.UserName,
		Action:        req.Command,
		Resource:      "slack_command",
		Outcome:       "success",
		Details:       map[string]interface{}{"command_text": req.Text, "channel": req.ChannelID},
		CorrelationID: req.CorrelationID,
	})
	
	// Execute command
	return sch.executeCommand(req)
}

// getRequiredPermission maps Slack commands to required permissions
func (sch *SlackCommandHandler) getRequiredPermission(command, text string) string {
	switch command {
	case "/portfolio", "/status":
		return PermissionViewPortfolio
	case "/risk", "/drawdown":
		return PermissionViewRisk
	case "/halt":
		if strings.Contains(text, "emergency") {
			return PermissionEmergencyHalt
		}
		return PermissionManualHalt
	case "/recovery", "/resume":
		return PermissionInitiateRecovery
	case "/config":
		return PermissionConfigChange
	case "/audit":
		return PermissionAuditAccess
	default:
		return PermissionViewPortfolio // Default to least privilege
	}
}

// createUnauthorizedResponse creates a response for unauthorized access attempts
func (sch *SlackCommandHandler) createUnauthorizedResponse(command string, err error) interface{} {
	return map[string]interface{}{
		"text": fmt.Sprintf("❌ Unauthorized: %s\n\nContact your administrator to request access to `%s`.", err.Error(), command),
		"response_type": "ephemeral",
	}
}

// executeCommand executes the authorized command
func (sch *SlackCommandHandler) executeCommand(req SlackRequest) (interface{}, error) {
	// This would delegate to specific command handlers based on the command
	// For now, return a placeholder response
	return map[string]interface{}{
		"text": fmt.Sprintf("✅ Command `%s` executed successfully by %s", req.Command, req.UserName),
		"response_type": "in_channel",
	}, nil
}