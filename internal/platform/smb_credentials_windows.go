//go:build windows

package platform

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	credentialTypeGeneric        = 1
	credentialTypeDomainPassword = 2
)

var (
	advapi32      = syscall.NewLazyDLL("advapi32.dll")
	procCredReadW = advapi32.NewProc("CredReadW")
	procCredFree  = advapi32.NewProc("CredFree")
)

type windowsCredential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

func readWindowsSMBCredential(source string) (string, []uint16, error) {
	target := windowsSMBCredentialTarget(source)
	if target == "" {
		return "", nil, fmt.Errorf("derive Windows Credential Manager target from %q", source)
	}
	var lastErr error
	for _, candidate := range windowsSMBCredentialCandidates(target) {
		for _, credentialType := range []uint32{credentialTypeDomainPassword, credentialTypeGeneric} {
			username, password, err := readWindowsCredential(candidate, credentialType)
			if err == nil {
				return username, password, nil
			}
			lastErr = err
		}
	}
	return "", nil, fmt.Errorf("read Generic Windows Credential Manager entry %q (create it with cmdkey /generic:%s): %w", target, target, lastErr)
}

func windowsSMBCredentialCandidates(target string) []string {
	return []string{target, "Domain:target=" + target, "LegacyGeneric:target=" + target}
}

func readWindowsCredential(target string, credentialType uint32) (string, []uint16, error) {
	targetPtr, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return "", nil, err
	}
	var credential *windowsCredential
	r1, _, callErr := procCredReadW.Call(
		uintptr(unsafe.Pointer(targetPtr)), uintptr(credentialType), 0,
		uintptr(unsafe.Pointer(&credential)))
	if r1 == 0 {
		if callErr == nil || errors.Is(callErr, syscall.Errno(0)) {
			callErr = errors.New("CredReadW failed")
		}
		return "", nil, callErr
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(credential)))
	if credential == nil || credential.UserName == nil || credential.CredentialBlob == nil || credential.CredentialBlobSize%2 != 0 {
		return "", nil, errors.New("Credential Manager entry has an invalid username or password blob")
	}
	passwordLength := int(credential.CredentialBlobSize / 2)
	password := make([]uint16, passwordLength+1)
	copy(password, unsafe.Slice((*uint16)(unsafe.Pointer(credential.CredentialBlob)), passwordLength))
	return windows.UTF16PtrToString(credential.UserName), password, nil
}

func windowsSMBCredentialTarget(source string) string {
	source = strings.TrimLeft(strings.TrimSpace(strings.ReplaceAll(source, `\`, "/")), "/")
	if separator := strings.IndexByte(source, '/'); separator >= 0 {
		source = source[:separator]
	}
	return strings.TrimSuffix(source, ".")
}

func zeroUTF16(value []uint16) {
	for index := range value {
		value[index] = 0
	}
}
