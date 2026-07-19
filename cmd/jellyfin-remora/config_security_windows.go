//go:build windows

package main

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var broadWindowsConfigReaders = map[string]string{
	"S-1-1-0":      "Everyone",
	"S-1-5-11":     "Authenticated Users",
	"S-1-5-32-545": "Builtin Users",
}

func validateConfigFileSecurity(string) error { return nil }

func configFileWarnings(path string) []string {
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return []string{fmt.Sprintf("cannot inspect configuration DACL: %v", err)}
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return []string{"configuration has no inspectable DACL"}
	}
	readMask := windows.ACCESS_MASK(windows.FILE_READ_DATA | windows.FILE_READ_ATTRIBUTES | windows.FILE_READ_EA | windows.GENERIC_READ)
	seen := map[string]bool{}
	var warnings []string
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil || ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		if ace.Mask&readMask == 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		name, broad := broadWindowsConfigReaders[sid]
		if broad && !seen[sid] {
			warnings = append(warnings, fmt.Sprintf("configuration DACL grants read access to %s (%s); restrict it to the service identity, administrators, and the operator", name, sid))
			seen[sid] = true
		}
	}
	return warnings
}
