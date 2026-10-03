//go:build windows

package tools

import (
	"slices"
	"testing"
)

func TestFileTransportArgumentsUseWindowsStdinDescriptor(t *testing.T) {
	for name, args := range map[string][]string{
		"technical":   technicalFileArguments(),
		"packet seek": packetSeekArguments(),
	} {
		if !slices.Contains(args, "-fd") {
			t.Errorf("%s arguments omit -fd: %q", name, args)
			continue
		}
		index := slices.Index(args, "-fd")
		if index+1 >= len(args) || args[index+1] != "0" {
			t.Errorf("%s -fd argument = %q, want literal Windows stdin descriptor 0", name, args)
		}
	}
}
