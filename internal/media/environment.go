package media

import (
	"fmt"
	"os"
	"strconv"
)

// Environment provides validated component configuration from environment variables.
type Environment struct{}

func (Environment) String(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}

	return fallback
}

func (Environment) Required(name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}

	return value, nil
}

func (environment Environment) Int(name string, fallback int) (int, error) {
	value := environment.String(name, "")
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}

	return parsed, nil
}

func (environment Environment) Bool(name string, fallback bool) (bool, error) {
	value := environment.String(name, "")
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", name, err)
	}

	return parsed, nil
}
