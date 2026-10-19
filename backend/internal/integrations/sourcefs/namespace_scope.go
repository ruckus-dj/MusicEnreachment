package sourcefs

import (
	"context"
	"errors"
)

// verifyNamespaceProbeScope proves the private probe directory has the same
// filename namespace rules as its actual parent before probe entries are made.
func verifyNamespaceProbeScope(ctx context.Context, parent, scratch OutputDirectory) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := verifyNativeNamespaceProbeScope(ctx, parent, scratch); err != nil {
		return errors.Join(ErrUnsupported, err)
	}
	return ctx.Err()
}

func unwrapNamespaceScopeDirectory(dir OutputDirectory) OutputDirectory {
	if native, ok := dir.(interface{ namespaceScopeDirectory() OutputDirectory }); ok {
		return native.namespaceScopeDirectory()
	}
	return dir
}
