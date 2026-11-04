package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func TestPublicationExecutorRejectsNilRepository(t *testing.T) {
	executor := NewPublicationExecutor(nil, nil, tools.Platform{}, publication.NewCoordinator(), uuid.New(), nil)
	if executor.repository != nil {
		t.Fatal("nil concrete repository was retained as a non-nil interface")
	}
	if err := executor.ExecutePublicationDelivery(context.Background(), uuid.New(), 1, 1); err == nil {
		t.Fatal("delivery with nil dependencies unexpectedly succeeded")
	}
}
