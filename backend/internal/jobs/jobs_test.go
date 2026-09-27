package jobs

import "testing"

func TestListenerConfigUsesOneDedicatedConnection(t *testing.T) {
	config, err := newListenerConfig("postgres://music:music@localhost:5432/music")
	if err != nil {
		t.Fatalf("newListenerConfig() error = %v", err)
	}
	if config.MinConns != 0 {
		t.Errorf("MinConns = %d, want 0", config.MinConns)
	}
	if config.MaxConns != 1 {
		t.Errorf("MaxConns = %d, want 1", config.MaxConns)
	}
}
