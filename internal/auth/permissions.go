package auth

import (
	"strings"

	"github.com/bwmarrin/discordgo"

	"DiscordAIChatbot/internal/config"
)

// PermissionChecker handles permission validation
type PermissionChecker struct {
	config            *config.Config
	adminIDs          map[string]struct{}
	allowedUserIDs    map[string]struct{}
	blockedUserIDs    map[string]struct{}
	allowedRoleIDs    map[string]struct{}
	blockedRoleIDs    map[string]struct{}
	allowedChannelIDs map[string]struct{}
	blockedChannelIDs map[string]struct{}
}

// NewPermissionChecker creates a new permission checker and pre-populates ID maps
func NewPermissionChecker(cfg *config.Config) *PermissionChecker {
	p := &PermissionChecker{
		config:            cfg,
		adminIDs:          make(map[string]struct{}),
		allowedUserIDs:    make(map[string]struct{}),
		blockedUserIDs:    make(map[string]struct{}),
		allowedRoleIDs:    make(map[string]struct{}),
		blockedRoleIDs:    make(map[string]struct{}),
		allowedChannelIDs: make(map[string]struct{}),
		blockedChannelIDs: make(map[string]struct{}),
	}

	for _, id := range cfg.Permissions.Users.AdminIDs {
		p.adminIDs[id] = struct{}{}
	}
	for _, id := range cfg.Permissions.Users.AllowedIDs {
		p.allowedUserIDs[id] = struct{}{}
	}
	for _, id := range cfg.Permissions.Users.BlockedIDs {
		p.blockedUserIDs[id] = struct{}{}
	}
	for _, id := range cfg.Permissions.Roles.AllowedIDs {
		p.allowedRoleIDs[id] = struct{}{}
	}
	for _, id := range cfg.Permissions.Roles.BlockedIDs {
		p.blockedRoleIDs[id] = struct{}{}
	}
	for _, id := range cfg.Permissions.Channels.AllowedIDs {
		p.allowedChannelIDs[id] = struct{}{}
	}
	for _, id := range cfg.Permissions.Channels.BlockedIDs {
		p.blockedChannelIDs[id] = struct{}{}
	}

	return p
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
	_, ok := p.adminIDs[userID]
	return ok
}

// checkUserPermissions validates user-level permissions
func (p *PermissionChecker) checkUserPermissions(userID string, isDM bool) bool {
	// Check if user is blocked
	if _, ok := p.blockedUserIDs[userID]; ok {
		return false
	}

	// Check if user is explicitly allowed
	if _, ok := p.allowedUserIDs[userID]; ok {
		return true
	}

	// For DMs, if no specific user permissions are set, allow by default
	if isDM {
		return len(p.allowedUserIDs) == 0
	}

	// For guild messages, check if we allow all users when no specific permissions
	return len(p.allowedUserIDs) == 0 && len(p.allowedRoleIDs) == 0
}

// checkRolePermissions validates role-level permissions
func (p *PermissionChecker) checkRolePermissions(roleIDs []string) bool {
	// Check if any role is blocked
	for _, roleID := range roleIDs {
		if _, ok := p.blockedRoleIDs[roleID]; ok {
			return false
		}
	}

	// Check if any role is explicitly allowed
	for _, roleID := range roleIDs {
		if _, ok := p.allowedRoleIDs[roleID]; ok {
			return true
		}
	}

	// If no specific role permissions are set, allow by default
	return len(p.allowedRoleIDs) == 0
}

// checkChannelPermissions validates channel-level permissions
func (p *PermissionChecker) checkChannelPermissions(channelIDs []string, isDM bool, userID string) bool {
	// For DMs, use different logic
	if isDM {
		return p.isAdmin(userID) || p.config.AllowDMs
	}

	// Check if any channel is blocked
	for _, channelID := range channelIDs {
		if _, ok := p.blockedChannelIDs[channelID]; ok {
			return false
		}
	}

	// Check if any channel is explicitly allowed
	for _, channelID := range channelIDs {
		if _, ok := p.allowedChannelIDs[channelID]; ok {
			return true
		}
	}

	// If no specific channel permissions are set, allow by default
	return len(p.allowedChannelIDs) == 0
}

// IsVisionModel checks if a model supports vision
func IsVisionModel(model string) bool {
	modelLower := strings.ToLower(model)
	// Added gpt-5 to support upcoming OpenAI GPT-5 family (including gpt-5-mini)
	visionTags := []string{"gpt-4", "gpt-5", "o3", "o4", "claude", "gemini", "gemma", "llama", "pixtral", "mistral", "vision", "vl"}

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
