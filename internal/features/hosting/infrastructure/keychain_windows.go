package infrastructure

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	hosting "github.com/macdipu/gitswitch/internal/features/hosting/domain"
)

// keychain uses the Windows Credential Manager (generic credentials named
// gitswitch:<account>), the same store Git Credential Manager uses.
type keychain struct{}

var (
	advapi32       = syscall.NewLazyDLL("advapi32.dll")
	procCredRead   = advapi32.NewProc("CredReadW")
	procCredWrite  = advapi32.NewProc("CredWriteW")
	procCredDelete = advapi32.NewProc("CredDeleteW")
	procCredFree   = advapi32.NewProc("CredFree")
)

const (
	credTypeGeneric         = 1
	credPersistLocalMachine = 2 // this user on this computer, kept across logons
	errorNotFound           = 1168
)

// credential is CREDENTIALW.
type credential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        syscall.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

func target(id string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(service + ":" + key(id))
	return p
}

func (keychain) Get(id string) (string, error) {
	var c *credential
	r, _, err := procCredRead.Call(uintptr(unsafe.Pointer(target(id))), credTypeGeneric, 0, uintptr(unsafe.Pointer(&c)))
	if r == 0 {
		if errors.Is(err, syscall.Errno(errorNotFound)) {
			return "", hosting.ErrNoToken
		}
		return "", fmt.Errorf("cannot read the Credential Manager: %v", err)
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(c)))
	return string(unsafe.Slice(c.CredentialBlob, c.CredentialBlobSize)), nil
}

func (keychain) Set(id, token string) error {
	blob := []byte(token)
	user, _ := syscall.UTF16PtrFromString(id)
	c := credential{
		Type:               credTypeGeneric,
		TargetName:         target(id),
		CredentialBlobSize: uint32(len(blob)),
		CredentialBlob:     &blob[0],
		Persist:            credPersistLocalMachine,
		UserName:           user,
	}
	if r, _, err := procCredWrite.Call(uintptr(unsafe.Pointer(&c)), 0); r == 0 {
		return fmt.Errorf("cannot save the token in the Credential Manager: %v", err)
	}
	return nil
}

func (keychain) Delete(id string) error {
	r, _, err := procCredDelete.Call(uintptr(unsafe.Pointer(target(id))), credTypeGeneric, 0)
	if r == 0 && !errors.Is(err, syscall.Errno(errorNotFound)) {
		return fmt.Errorf("cannot remove the token from the Credential Manager: %v", err)
	}
	return nil
}
