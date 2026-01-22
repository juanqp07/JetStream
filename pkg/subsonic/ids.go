package subsonic

import (
	"strings"
)

// ParseID parses a Subsonic ID string.
// Formats:
// - "ext-{provider}-{type}-{id}" (e.g., "ext-deezer-song-1234")
// - "ext-{provider}-{id}" (Legacy, assumes type based on context if possible, but strict parsing is preferred)
// - "{id}" (Local ID)
func ParseID(id string) (isExternal bool, provider, mediaType, externalID string) {
	if !strings.HasPrefix(id, "ext-") {
		return false, "", "", id
	}

	parts := strings.Split(id, "-")

	// Format: ext-{provider}-{type}-{id}
	if len(parts) >= 4 {
		return true, parts[1], parts[2], strings.Join(parts[3:], "-")
	}

	// Fallback/Legacy: ext-{provider}-{id} (Try to guess or return empty type)
	if len(parts) >= 3 {
		return true, parts[1], "", strings.Join(parts[2:], "-")
	}

	return false, "", "", id
}

// BuildID creates a standardized external ID.
func BuildID(provider, mediaType, externalID string) string {
	return "ext-" + provider + "-" + mediaType + "-" + externalID
}
