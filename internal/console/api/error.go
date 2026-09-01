package api

import (
	"fmt"

	"github.com/traP-jp/kinugasa-recording/internal/console/application"
)

func errInvalidRequest(err error) error {
	return fmt.Errorf("%w: %v", application.ErrInvalidArgument, err)
}
