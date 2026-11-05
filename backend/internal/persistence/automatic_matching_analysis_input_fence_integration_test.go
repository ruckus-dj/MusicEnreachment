//go:build integration

package persistence

import (
	"encoding/json"
	"github.com/google/uuid"
	"testing"
)

func byStepFromInputs(inputs []automaticArtistAnalysisInput, step string) automaticArtistAnalysisInput {
	for _, input := range inputs {
		if input.Step == step {
			return input
		}
	}
	return automaticArtistAnalysisInput{}
}

func automaticArtistCaptureDigestForTest(t *testing.T, inputs []automaticArtistAnalysisInput) [32]byte {
	t.Helper()
	capture := automaticArtistTargetCapture{
		SchemaVersion: 2,
		GroupID:       uuid.Nil,
		GroupRevision: "revision",
		Members: []automaticArtistTargetMember{{
			VariantID: uuid.Nil, AnalysisInputs: inputs,
		}},
	}
	encoded, err := json.Marshal(capture)
	if err != nil {
		t.Fatal(err)
	}
	_, digest, err := automaticArtistTargetCaptureDigest(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
