//go:build windows

package setup

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

func (platform *WindowsPlatform) CheckParentElevation() error {
	return checkWindowsParentToken(windows.GetCurrentProcessToken())
}

func checkWindowsParentToken(token windows.Token) error {
	elevationType, err := setupTokenUint32(token, windows.TokenElevationType)
	if err != nil {
		return err
	}
	sessionID, err := setupTokenUint32(token, windows.TokenSessionId)
	if err != nil {
		return err
	}
	user, err := token.GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		return errors.New("inspect setup account")
	}
	serviceAccount := user.User.Sid.IsWellKnown(windows.WinLocalSystemSid) ||
		user.User.Sid.IsWellKnown(windows.WinLocalServiceSid) ||
		user.User.Sid.IsWellKnown(windows.WinNetworkServiceSid)
	return validateWindowsParentToken(elevationType, sessionID, serviceAccount)
}

func setupTokenUint32(token windows.Token, informationClass uint32) (uint32, error) {
	var value, returned uint32
	size := uint32(unsafe.Sizeof(value))
	if err := windows.GetTokenInformation(token, informationClass, (*byte)(unsafe.Pointer(&value)), size, &returned); err != nil || returned != size {
		return 0, errors.New("inspect setup Windows token")
	}
	return value, nil
}

func validateWindowsParentToken(elevationType, sessionID uint32, serviceAccount bool) error {
	if sessionID == 0 || serviceAccount {
		return errors.New("run Inevitable Mobile Relay Setup from your signed-in Windows desktop")
	}
	// TOKEN_ELEVATION_TYPE distinguishes an explicitly elevated split token
	// from a normal, unsplit Administrator desktop (common on Windows Server).
	// An unsplit token has no limited counterpart to relaunch with. Preserve
	// that account's existing policy; never change UAC or synthesize a token.
	switch elevationType {
	case 1, 3: // TokenElevationTypeDefault, TokenElevationTypeLimited
		return nil
	case 2: // TokenElevationTypeFull
		return errors.New("close Inevitable Mobile Relay Setup and double-click it from your normal Windows desktop without Run as administrator")
	default:
		return errors.New("unsupported setup Windows elevation type")
	}
}
