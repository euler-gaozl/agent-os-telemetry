package telemetry

import (
	"context"

	"errors"
)

func (s *Store) Secret(ctx context.Context, name string) (string, error) {
	if s.Remote == nil || s.Remote.RegistryURL == "" {
		return "", errors.New("registry credential client required")
	}
	return s.Remote.Secret(ctx, name)
}
