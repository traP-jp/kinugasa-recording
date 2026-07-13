// Package validation contains validation shared by the operator HTTP API.
package validation

import (
	"fmt"
	"regexp"

	recordingv1alpha1 "github.com/comavius/kinugasa-recording/api/recording/v1alpha1"
)

var namePattern = regexp.MustCompile(recordingv1alpha1.NamePattern)

// Name validates a user-defined session, camera, or take name.
func Name(name string) error {
	if len(name) == 0 {
		return fmt.Errorf("name must not be empty")
	}
	if len(name) > recordingv1alpha1.MaxNameSize {
		return fmt.Errorf("name must be at most %d bytes", recordingv1alpha1.MaxNameSize)
	}
	if !namePattern.MatchString(name) {
		return fmt.Errorf("name must contain only ASCII letters, digits, and hyphens")
	}

	return nil
}
