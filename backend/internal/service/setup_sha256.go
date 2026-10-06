package service

import "context"

func (s *SetupService) GetSHA256Enabled(ctx context.Context) (bool, error) {
	return s.registry.GetSHA256Enabled(ctx)
}

func (s *SetupService) SetSHA256Enabled(ctx context.Context, enabled bool) error {
	return s.registry.SetSHA256Enabled(ctx, enabled)
}
