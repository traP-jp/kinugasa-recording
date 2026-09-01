package domain

import "time"

type SessionState string

const (
	SessionStateActive   SessionState = "active"
	SessionStateInactive SessionState = "inactive"
)

type Session struct {
	ID        SessionID
	Name      string
	State     SessionState
	CreatedAt time.Time
}

func (s Session) Validate() error {
	if err := validateID("id", string(s.ID)); err != nil {
		return err
	}
	if err := validateName("name", s.Name); err != nil {
		return err
	}
	if s.State != SessionStateActive && s.State != SessionStateInactive {
		return invalid("state", "must be active or inactive")
	}
	return validateTime("createdAt", s.CreatedAt)
}
