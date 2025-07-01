package auth

import (
	"strings"

	"github.com/bwmarrin/discordgo"

	"DiscordAIChatbot/internal/config"
)

// PermissionChecker handles permission validation
type PermissionChecker struct {
	config *config.Config
}

// NewPermissionChecker creates a new permission checker
func NewPermissionChecker(cfg *config.Config) *PermissionChecker {
	return &PermissionChecker{config: cfg}
}

// CheckPermissions checks if a user has permission to use the bot
func (p *PermissionChecker) CheckPermissions(m *discordgo.MessageCreate) bool {
	isDM := m.ChannelID != "" && m.GuildID == ""

	// Get user roles (empty for DMs)
	var roleIDs []string
	if m.Member != nil {
		roleIDs = m.Member.Roles
	}

	// Get channel hierarchy IDs
	channelIDs := []string{m.ChannelID}
	// Note: For thread parent and category, we'd need additional API calls

	userID := m.Author.ID

	// Check if user is admin
	if p.isAdmin(userID) {
		return true
	}

	// Check DM permissions
	if isDM && !p.config.AllowDMs {
		return false
	}

	// Check user permissions
	if !p.checkUserPermissions(userID, isDM) {
		return false
	}

	// Check role permissions (only for guild messages)
	if !isDM && !p.checkRolePermissions(roleIDs) {
		return false
	}

	// Check channel permissions
	if !p.checkChannelPermissions(channelIDs, isDM, userID) {
		return false
	}

	return true
}

// isAdmin checks if user is an admin
func (p *PermissionChecker) isAdmin(userID string) bool {
	return p.contains(p.config.Permissions.Users.AdminIDs, userID)
}

// checkUserPermissions validates user-level permissions
func (p *PermissionChecker) checkUserPermissions(userID string, isDM bool) bool {
	perms := p.config.Permissions.Users

	// Check if user is blocked
	if p.contains(perms.BlockedIDs, userID) {
		return false
	}

	// Check if user is explicitly allowed
	if p.contains(perms.AllowedIDs, userID) {
		return true
	}

	// For DMs, if no specific user permissions are set, allow by default
	if isDM {
		return len(perms.AllowedIDs) == 0
	}

	// For guild messages, check if we allow all users when no specific permissions
	return len(perms.AllowedIDs) == 0 && len(p.config.Permissions.Roles.AllowedIDs) == 0
}

// checkRolePermissions validates role-level permissions
func (p *PermissionChecker) checkRolePermissions(roleIDs []string) bool {
	perms := p.config.Permissions.Roles

	// Check if any role is blocked
	for _, roleID := range roleIDs {
		if p.contains(perms.BlockedIDs, roleID) {
			return false
		}
	}

	// Check if any role is explicitly allowed
	for _, roleID := range roleIDs {
		if p.contains(perms.AllowedIDs, roleID) {
			return true
		}
	}

	// If no specific role permissions are set, allow by default
	return len(perms.AllowedIDs) == 0
}

// checkChannelPermissions validates channel-level permissions
func (p *PermissionChecker) checkChannelPermissions(channelIDs []string, isDM bool, userID string) bool {
	perms := p.config.Permissions.Channels

	// For DMs, use different logic
	if isDM {
		return p.isAdmin(userID) || p.config.AllowDMs
	}

	// Check if any channel is blocked
	for _, channelID := range channelIDs {
		if p.contains(perms.BlockedIDs, channelID) {
			return false
		}
	}

	// Check if any channel is explicitly allowed
	for _, channelID := range channelIDs {
		if p.contains(perms.AllowedIDs, channelID) {
			return true
		}
	}

	// If no specific channel permissions are set, allow by default
	return len(perms.AllowedIDs) == 0
}

// contains checks if a slice contains a string
func (p *PermissionChecker) contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// IsVisionModel checks if a model supports vision
func IsVisionModel(model string) bool {
	modelLower := strings.ToLower(model)
	visionTags := []string{"gpt-4", "o3", "o4", "claude", "gemini", "gemma", "llama", "pixtral", "mistral", "vision", "vl"}

	for _, tag := range visionTags {
		if strings.Contains(modelLower, tag) {
			return true
		}
	}
	return false
}

// SupportsUsernames checks if a provider supports usernames
func SupportsUsernames(providerModel string) bool {
	modelLower := strings.ToLower(providerModel)
	supportedProviders := []string{"openai", "x-ai"}

	for _, provider := range supportedProviders {
		if strings.HasPrefix(modelLower, provider) {
			return true
		}
	}
	return false
}
