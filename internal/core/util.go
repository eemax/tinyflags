package core

import "strings"

// FirstNonEmpty returns the first non-empty string from the provided values.
func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// SupportedFormat reports whether format is a recognized output format.
func SupportedFormat(format string) bool {
	return format == "text" || format == "json"
}

// JoinNonEmpty joins non-empty, trimmed parts with the given separator.
func JoinNonEmpty(sep string, parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			filtered = append(filtered, part)
		}
	}
	return strings.Join(filtered, sep)
}
