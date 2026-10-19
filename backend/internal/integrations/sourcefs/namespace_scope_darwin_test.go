//go:build darwin

package sourcefs

import (
	"encoding/binary"
	"testing"
	"unsafe"
)

// darwinVolumeCapabilitiesSetBytes is the byte width of vol_capabilities_set_t,
// a u_int32_t[4] in the Darwin SDK.
const darwinVolumeCapabilitiesSetBytes = 4 * 4

// darwinFakeVolumeCapabilities fabricates the raw vol_capabilities_attr_t the
// kernel would return for the format capability set: a u_int32_t[4]
// "capabilities" block followed by a u_int32_t[4] "valid" block. Building the
// native byte layout keeps the test honest about the SDK's indexing and widths.
func darwinFakeVolumeCapabilities(formatCapabilities, formatValid uint32) darwinVolumeCapabilities {
	var raw [unsafe.Sizeof(darwinVolumeCapabilities{})]byte
	offset := darwinVolCapabilitiesFormat * 4
	binary.LittleEndian.PutUint32(raw[offset:], formatCapabilities)
	binary.LittleEndian.PutUint32(raw[darwinVolumeCapabilitiesSetBytes+offset:], formatValid)
	return *(*darwinVolumeCapabilities)(unsafe.Pointer(&raw[0]))
}

func TestDarwinVolumeCapabilitiesBufferLayout(t *testing.T) {
	var caps darwinVolumeCapabilities
	if got, want := unsafe.Sizeof(caps), uintptr(darwinVolumeCapabilitiesSetBytes*2); got != want {
		t.Fatalf("darwinVolumeCapabilities size = %d, want %d (capabilities + valid)", got, want)
	}
	if unsafe.Offsetof(caps.Capabilities) != 0 || unsafe.Offsetof(caps.Valid) != darwinVolumeCapabilitiesSetBytes {
		t.Fatalf("darwinVolumeCapabilities offsets = [%d %d], want [0 %d]",
			unsafe.Offsetof(caps.Capabilities), unsafe.Offsetof(caps.Valid), darwinVolumeCapabilitiesSetBytes)
	}
}

func TestDarwinNamespaceScopeCapabilityCheck(t *testing.T) {
	const caseBits = darwinVolFmtCaseSensitive | darwinVolFmtCasePreserving
	const unrelatedBits = 0x00000001 | 0x00000002 // persistent object IDs + symbolic links

	cases := []struct {
		name      string
		parent    darwinVolumeCapabilities
		scratch   darwinVolumeCapabilities
		wantError bool
	}{
		{
			name:      "unrelated bits 1 and 2 are not valid case capabilities",
			parent:    darwinFakeVolumeCapabilities(unrelatedBits, unrelatedBits),
			scratch:   darwinFakeVolumeCapabilities(unrelatedBits, unrelatedBits),
			wantError: true,
		},
		{
			name:    "case-insensitive parent and scratch agree",
			parent:  darwinFakeVolumeCapabilities(darwinVolFmtCasePreserving, caseBits),
			scratch: darwinFakeVolumeCapabilities(darwinVolFmtCasePreserving, caseBits),
		},
		{
			name:    "case-sensitive parent and scratch agree",
			parent:  darwinFakeVolumeCapabilities(caseBits, caseBits),
			scratch: darwinFakeVolumeCapabilities(caseBits, caseBits),
		},
		{
			name:      "case-sensitivity differs from parent",
			parent:    darwinFakeVolumeCapabilities(caseBits, caseBits),
			scratch:   darwinFakeVolumeCapabilities(darwinVolFmtCasePreserving, caseBits),
			wantError: true,
		},
		{
			name:      "scratch does not report the case bits as valid",
			parent:    darwinFakeVolumeCapabilities(caseBits, caseBits),
			scratch:   darwinFakeVolumeCapabilities(caseBits, 0),
			wantError: true,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := darwinNamespaceScopeError(test.parent, test.scratch)
			if test.wantError && err == nil {
				t.Fatal("expected a namespace scope error, got nil")
			}
			if !test.wantError && err != nil {
				t.Fatalf("unexpected namespace scope error: %v", err)
			}
		})
	}
}
