package persistence

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestFailPublicationSourceCleanupExplicitRetryDeliveryRejectsMalformedGuardBeforeDatabase(t *testing.T) {
	t.Parallel()
	valid := PublicationSourceCleanupDeliveryGuard{
		OperationID: uuid.New(), ApplicationAttempt: 1, RiverJobID: 1, RiverAttempt: 1,
		ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: uuid.NewString(),
		Epoch: uuid.New(), PositionID: uuid.New(),
	}
	for _, alter := range []struct {
		name   string
		change func(*PublicationSourceCleanupDeliveryGuard)
	}{
		{"zero operation", func(g *PublicationSourceCleanupDeliveryGuard) { g.OperationID = uuid.Nil }},
		{"zero claim", func(g *PublicationSourceCleanupDeliveryGuard) { g.ClaimUUID = uuid.Nil }},
		{"empty token", func(g *PublicationSourceCleanupDeliveryGuard) { g.DeliveryToken = "" }},
		{"zero application attempt", func(g *PublicationSourceCleanupDeliveryGuard) { g.ApplicationAttempt = 0 }},
		{"zero river job", func(g *PublicationSourceCleanupDeliveryGuard) { g.RiverJobID = 0 }},
		{"zero river attempt", func(g *PublicationSourceCleanupDeliveryGuard) { g.RiverAttempt = 0 }},
	} {
		t.Run(alter.name, func(t *testing.T) {
			guard := valid
			alter.change(&guard)
			failed, err := new(SetupManagerRepository).FailPublicationSourceCleanupExplicitRetryDelivery(context.Background(), guard, "safe failure")
			if err == nil || failed || !strings.Contains(err.Error(), "valid guard and safe error are required") {
				t.Fatalf("malformed guard failed=%t err=%v", failed, err)
			}
		})
	}
}
