package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	resourceNamePattern = regexp.MustCompile(`^[a-z](?:[a-z0-9-]{0,30}[a-z0-9])?$`)
	uuidPattern         = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

// ValidationError identifies a domain invariant violation.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Reason)
}

func invalid(field, reason string) error {
	return &ValidationError{Field: field, Reason: reason}
}

func validateName(field, value string) error {
	if !resourceNamePattern.MatchString(value) {
		return invalid(field, "must match "+resourceNamePattern.String())
	}
	return nil
}

func validateID(field, value string) error {
	if !uuidPattern.MatchString(value) {
		return invalid(field, "must be a canonical UUID")
	}
	return nil
}

func validateTime(field string, value time.Time) error {
	if value.IsZero() {
		return invalid(field, "must be set")
	}
	return nil
}

func validateErrorReason(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return invalid(field, "must not be empty")
	}
	return nil
}
