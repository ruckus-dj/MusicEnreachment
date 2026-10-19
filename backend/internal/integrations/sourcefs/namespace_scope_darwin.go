//go:build darwin

package sourcefs

import (
	"context"
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Darwin volume capability constants from <sys/attr.h>. VOL_CAPABILITIES_FORMAT
// is index zero of vol_capabilities_attr_t, and the filename format case bits
// there are 0x100/0x200; the low-order 0x1/0x2 bits belong to persistent object
// IDs and symbolic links, not to case sensitivity.
const (
	darwinAttrVolCapabilities   = 0x00020000
	darwinVolCapabilitiesFormat = 0
	darwinVolFmtCaseSensitive   = 0x00000100
	darwinVolFmtCasePreserving  = 0x00000200
)

type darwinAttrList struct {
	BitmapCount uint16
	Reserved    uint16
	CommonAttr  uint32
	VolAttr     uint32
	DirAttr     uint32
	FileAttr    uint32
	ForkAttr    uint32
}

type darwinVolumeCapabilities struct {
	Capabilities [4]uint32
	Valid        [4]uint32
}

func verifyNativeNamespaceProbeScope(ctx context.Context, parent, scratch OutputDirectory) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	parent = unwrapNamespaceScopeDirectory(parent)
	scratch = unwrapNamespaceScopeDirectory(scratch)
	p, ok := parent.(*unixOutputDir)
	if !ok {
		return errors.New("sourcefs: native parent capability unavailable")
	}
	s, ok := scratch.(*unixOutputDir)
	if !ok {
		return errors.New("sourcefs: native scratch capability unavailable")
	}
	pi, err := p.Identity(ctx)
	if err != nil {
		return err
	}
	si, err := s.Identity(ctx)
	if err != nil {
		return err
	}
	if pi.Volume != si.Volume {
		return errors.New("sourcefs: probe crossed filesystem volume")
	}
	pfd, err := duplicateOutputDirFD(p)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := unix.Close(pfd); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("sourcefs: close parent namespace probe descriptor: %w", closeErr))
		}
	}()
	sfd, err := duplicateOutputDirFD(s)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := unix.Close(sfd); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("sourcefs: close scratch namespace probe descriptor: %w", closeErr))
		}
	}()
	pc, err := darwinVolumeCapabilitiesForFD(pfd)
	if err != nil {
		return err
	}
	sc, err := darwinVolumeCapabilitiesForFD(sfd)
	if err != nil {
		return err
	}
	if err := darwinNamespaceScopeError(pc, sc); err != nil {
		return err
	}
	return ctx.Err()
}

// darwinNamespaceScopeError reports whether two volumes' filename format
// capabilities prove the probe directory inherits its parent's namespace rules.
// It is a pure function of the decoded capability bitmaps, so the scope rule can
// be tested against a synthetic vol_capabilities_attr_t without a real volume.
func darwinNamespaceScopeError(parent, scratch darwinVolumeCapabilities) error {
	const known = darwinVolFmtCaseSensitive | darwinVolFmtCasePreserving
	parentValid := parent.Valid[darwinVolCapabilitiesFormat]
	scratchValid := scratch.Valid[darwinVolCapabilitiesFormat]
	if parentValid&known != known || scratchValid&known != known {
		return errors.New("sourcefs: filesystem does not report complete filename format capabilities")
	}
	if parent.Capabilities[darwinVolCapabilitiesFormat]&known != scratch.Capabilities[darwinVolCapabilitiesFormat]&known {
		return fmt.Errorf("sourcefs: probe filename format capabilities differ from parent")
	}
	return nil
}

func darwinVolumeCapabilitiesForFD(fd int) (darwinVolumeCapabilities, error) {
	list := darwinAttrList{BitmapCount: 5, VolAttr: darwinAttrVolCapabilities}
	var result struct {
		Length uint32
		Caps   darwinVolumeCapabilities
	}
	if err := darwinFGetAttrList(fd, unsafe.Pointer(&list), unsafe.Pointer(&result), unsafe.Sizeof(result)); err != nil {
		return darwinVolumeCapabilities{}, fmt.Errorf("sourcefs: query Darwin volume capabilities: %w", err)
	}
	if result.Length < uint32(unsafe.Sizeof(result)) {
		return darwinVolumeCapabilities{}, errors.New("sourcefs: incomplete Darwin volume capability response")
	}
	return result.Caps, nil
}
