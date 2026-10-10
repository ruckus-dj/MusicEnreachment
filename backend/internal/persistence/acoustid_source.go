package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

const (
	acoustIDOfficialEndpoint = "https://api.acoustid.org/v2/lookup"
	acoustIDProviderCode     = "acoustid"
	acoustIDSourceIdentity   = "acoustid-official-v1"
)

// ensureAcoustIDProviderSource is shared by credential writes and lookup
// admission. Its deterministic endpoint namespace and source ID do not depend
// on credential revision, so rotation never splits accepted cache identity.
func ensureAcoustIDProviderSource(ctx context.Context, tx bun.Tx) (ProviderSource, error) {
	providerID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("acoustid-provider"))
	sourceID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("acoustid-source:"+acoustIDOfficialEndpoint))
	namespaceHash := sha256.Sum256([]byte(acoustIDOfficialEndpoint))
	namespace := "acoustid:" + hex.EncodeToString(namespaceHash[:])
	if _, err := tx.NewRaw(`INSERT INTO provider(id,code) VALUES(?,?) ON CONFLICT(code) DO NOTHING`, providerID, acoustIDProviderCode).Exec(ctx); err != nil {
		return ProviderSource{}, err
	}
	if err := tx.NewRaw(`SELECT id FROM provider WHERE code=?`, acoustIDProviderCode).Scan(ctx, &providerID); err != nil {
		return ProviderSource{}, err
	}
	if _, err := tx.NewRaw(`INSERT INTO provider_source(id,provider_id,namespace,endpoint,configuration_identity)
		VALUES(?,?,?,?,?) ON CONFLICT(provider_id,namespace) DO UPDATE SET endpoint=EXCLUDED.endpoint,
		configuration_identity=EXCLUDED.configuration_identity,updated_at=now()`, sourceID, providerID, namespace, acoustIDOfficialEndpoint, acoustIDSourceIdentity).Exec(ctx); err != nil {
		return ProviderSource{}, err
	}
	var source ProviderSource
	err := tx.NewRaw(`SELECT id,provider_id,namespace,endpoint,configuration_identity FROM provider_source
		WHERE provider_id=? AND namespace=?`, providerID, namespace).Scan(ctx, &source)
	return source, err
}
