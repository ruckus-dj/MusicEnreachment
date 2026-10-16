//go:build windows

package testpostgres

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ensureCurrentUserDefaultDACL grants the current user full access to objects
// created with this process's default DACL. Embedded PostgreSQL creates
// restricted child-process tokens that disable Administrators; without a
// direct user ACE, child processes may be unable to use pipes or start.
//
// This changes the test process token only, is intentionally process-wide,
// and is not restored: later restricted child tokens also need the repaired
// default DACL. A null DACL already grants unrestricted access and is left
// unchanged. Existing ACL entries are preserved.
func ensureCurrentUserDefaultDACL() error {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|windows.TOKEN_ADJUST_DEFAULT, &token); err != nil {
		return err
	}
	defer token.Close()

	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}

	var required uint32
	err = windows.GetTokenInformation(token, windows.TokenDefaultDacl, nil, 0, &required)
	if err != windows.ERROR_INSUFFICIENT_BUFFER {
		return err
	}
	buffer := make([]byte, required)
	if err = windows.GetTokenInformation(token, windows.TokenDefaultDacl, &buffer[0], uint32(len(buffer)), &required); err != nil {
		return err
	}
	defaultDACL := (*tokenDefaultDACL)(unsafe.Pointer(&buffer[0]))
	if defaultDACL.DefaultDacl == nil {
		// A null DACL is not the same as an empty DACL: it permits all access.
		return nil
	}

	var pinner runtime.Pinner
	pinner.Pin(user.User.Sid)
	defer pinner.Unpin()
	entries := []windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
		},
	}}
	acl, err := windows.ACLFromEntries(entries, defaultDACL.DefaultDacl)
	if err != nil {
		return err
	}
	updated := tokenDefaultDACL{DefaultDacl: acl}
	pinner.Pin(acl)
	pinner.Pin(&updated)
	return windows.SetTokenInformation(token, windows.TokenDefaultDacl, (*byte)(unsafe.Pointer(&updated)), uint32(unsafe.Sizeof(updated)))
}

type tokenDefaultDACL struct {
	DefaultDacl *windows.ACL
}
