package storage

import "fmt"

type InvalidKeyError struct {
	Key    string
	Reason string
}

func (e *InvalidKeyError) Error() string {
	return fmt.Sprintf("storage: invalid key %q: %s", e.Key, e.Reason)
}

type ConfigError struct {
	Backend string
	Field   string
	Reason  string
}

func (e *ConfigError) Error() string {
	if e.Field == "" {
		return fmt.Sprintf("storage: invalid config for backend %q: %s", e.Backend, e.Reason)
	}
	return fmt.Sprintf("storage: invalid config for backend %q field %q: %s", e.Backend, e.Field, e.Reason)
}
