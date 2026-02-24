package storage

import "fmt"

type InvalidKeyError struct {
	Key    string
	Reason string
}

func (e *InvalidKeyError) Error() string {
	if e == nil {
		return "invalid storage key"
	}
	if e.Reason == "" {
		return fmt.Sprintf("invalid storage key %q", e.Key)
	}
	return fmt.Sprintf("invalid storage key %q: %s", e.Key, e.Reason)
}

type ConfigError struct {
	Field   string
	Message string
}

func (e *ConfigError) Error() string {
	if e == nil {
		return "storage config error"
	}
	if e.Field == "" {
		return fmt.Sprintf("storage config error: %s", e.Message)
	}
	return fmt.Sprintf("storage config error for %s: %s", e.Field, e.Message)
}
