package service

import "context"

func (s *SetupService) SetSHA256Enabled(ctx context.Context, enabled bool) error {
	return s.registry.SetSHA256Enabled(ctx, enabled)
}
