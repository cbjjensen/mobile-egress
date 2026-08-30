//go:build windows

package setup

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	messageBoxYesNo       = 0x00000004
	messageBoxIconWarning = 0x00000030
	messageBoxIconError   = 0x00000010
	messageBoxTopmost     = 0x00040000
	messageBoxYes         = 6
	seeMaskNoCloseProcess = 0x00000040
	showNormal            = 1
	waitTimeout           = 0x00000102
	commonShortcutPath    = `C:\ProgramData\Microsoft\Windows\Start Menu\Programs\Mobile Egress.lnk`
)

var (
	user32          = windows.NewLazySystemDLL("user32.dll")
	messageBoxW     = user32.NewProc("MessageBoxW")
	shell32         = windows.NewLazySystemDLL("shell32.dll")
	shellExecuteExW = shell32.NewProc("ShellExecuteExW")
)

type WindowsPlatform struct{}

func NewWindowsPlatform() *WindowsPlatform { return &WindowsPlatform{} }

func (platform *WindowsPlatform) IsElevated() (bool, error) {
	return windows.GetCurrentProcessToken().IsElevated(), nil
}

func (platform *WindowsPlatform) Confirm(fingerprint string) (bool, error) {
	message := "Mobile Egress Setup will trust and install software signed by this publisher certificate.\n\n" +
		"SHA-256 publisher fingerprint:\n" + fingerprint + "\n\n" +
		"Compare this fingerprint with the value shared separately by the publisher. Continue only if every pair matches.\n\nContinue?"
	result, err := showMessageBox(message, "Mobile Egress Setup", messageBoxYesNo|messageBoxIconWarning|messageBoxTopmost)
	if err != nil {
		return false, err
	}
	return result == messageBoxYes, nil
}

func (platform *WindowsPlatform) ElevateAndWait(executable, nonce string) error {
	if !noncePattern.MatchString(nonce) {
		return errors.New("setup request nonce is invalid")
	}
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return err
	}
	parameters, err := windows.UTF16PtrFromString("--internal-elevated-install " + nonce)
	if err != nil {
		return err
	}
	directory, err := windows.UTF16PtrFromString(filepath.Dir(executable))
	if err != nil {
		return err
	}
	info := shellExecuteInfo{
		Mask:       seeMaskNoCloseProcess,
		Verb:       verb,
		File:       file,
		Parameters: parameters,
		Directory:  directory,
		Show:       showNormal,
	}
	info.Size = uint32(unsafe.Sizeof(info))
	result, _, callErr := shellExecuteExW.Call(uintptr(unsafe.Pointer(&info)))
	runtime.KeepAlive(info)
	if result == 0 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return callErr
		}
		return errors.New("start elevated setup")
	}
	if info.Process == 0 {
		return errors.New("elevated setup process handle is unavailable")
	}
	defer windows.CloseHandle(info.Process)
	event, err := windows.WaitForSingleObject(info.Process, uint32((10*time.Minute)/time.Millisecond))
	if err != nil {
		return err
	}
	if event == waitTimeout {
		return errors.New("elevated setup timed out")
	}
	return nil
}

func (platform *WindowsPlatform) Launch(executable string) error {
	want := filepath.Join(InstallRoot, ControllerExecutableName)
	if !strings.EqualFold(filepath.Clean(executable), filepath.Clean(want)) {
		return errors.New("installed controller path is invalid")
	}
	return exec.Command(executable).Start()
}

func (platform *WindowsPlatform) EnsureTrust(identity Identity) (TrustChanges, error) {
	changes := TrustChanges{}
	rootAdded, err := ensureCertificateInMachineStore("Root", identity)
	if err != nil {
		return changes, err
	}
	changes.RootAdded = rootAdded
	publisherAdded, err := ensureCertificateInMachineStore("TrustedPublisher", identity)
	if err != nil {
		if rootAdded {
			_ = removeExactCertificateFromMachineStore("Root", identity)
		}
		return TrustChanges{}, err
	}
	changes.TrustedPublisherAdded = publisherAdded
	return changes, nil
}

func (platform *WindowsPlatform) RollbackTrust(identity Identity, changes TrustChanges) error {
	var rollbackErrors []error
	if changes.TrustedPublisherAdded {
		if err := removeExactCertificateFromMachineStore("TrustedPublisher", identity); err != nil {
			rollbackErrors = append(rollbackErrors, err)
		}
	}
	if changes.RootAdded {
		if err := removeExactCertificateFromMachineStore("Root", identity); err != nil {
			rollbackErrors = append(rollbackErrors, err)
		}
	}
	return errors.Join(rollbackErrors...)
}

func (platform *WindowsPlatform) VerifyAuthenticode(path string, identity Identity) error {
	const script = `$ErrorActionPreference = 'Stop'
$signature = Get-AuthenticodeSignature -LiteralPath $env:MOBILE_EGRESS_SIGNATURE_PATH
$certificate = $signature.SignerCertificate
$sha256 = if ($null -eq $certificate) { '' } else {
  $hasher = [System.Security.Cryptography.SHA256]::Create()
  try { ([BitConverter]::ToString($hasher.ComputeHash($certificate.RawData))).Replace('-', '') } finally { $hasher.Dispose() }
}
$result = [ordered]@{
  status = [string]$signature.Status
  thumbprint = if ($null -eq $certificate) { '' } else { $certificate.Thumbprint }
  certificateSha256 = $sha256
  certificateBase64 = if ($null -eq $certificate) { '' } else { [Convert]::ToBase64String($certificate.RawData) }
  timestamped = $null -ne $signature.TimeStamperCertificate
}
[Console]::Out.Write(($result | ConvertTo-Json -Compress))`
	powershellPath, err := systemPowerShellPath()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, powershellPath, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script)
	command.Env = append(os.Environ(), "MOBILE_EGRESS_SIGNATURE_PATH="+path)
	output, err := command.Output()
	if err != nil || len(output) == 0 || len(output) > 16<<10 {
		return errors.New("Windows Authenticode inspection failed")
	}
	return validateAuthenticodeResult(output, identity)
}

func (platform *WindowsPlatform) Install(files []InstallFile, identity Identity) error {
	wantNames := []string{ControllerExecutableName, AdminExecutableName, RelayExecutableName}
	if len(files) != len(wantNames) {
		return errors.New("install file set is invalid")
	}
	for index, name := range wantNames {
		if filepath.Base(files[index].Source) != name || !strings.EqualFold(filepath.Clean(files[index].Destination), filepath.Join(InstallRoot, name)) {
			return errors.New("install file set is invalid")
		}
	}
	return installVerifiedFiles(files, func(path string) error {
		return platform.VerifyAuthenticode(path, identity)
	})
}

func (platform *WindowsPlatform) CreateShortcut(controllerPath string) error {
	want := filepath.Join(InstallRoot, ControllerExecutableName)
	if !strings.EqualFold(filepath.Clean(controllerPath), filepath.Clean(want)) {
		return errors.New("shortcut target is invalid")
	}
	const script = `$ErrorActionPreference = 'Stop'
$shortcutPath = 'C:\ProgramData\Microsoft\Windows\Start Menu\Programs\Mobile Egress.lnk'
$targetPath = 'C:\Program Files\MobileEgress\Controller\mobile-egress-windows.exe'
$shell = New-Object -ComObject WScript.Shell
$shortcut = $shell.CreateShortcut($shortcutPath)
$shortcut.TargetPath = $targetPath
$shortcut.WorkingDirectory = 'C:\Program Files\MobileEgress\Controller'
$shortcut.Description = 'Mobile Egress Controller'
$shortcut.Save()`
	powershellPath, err := systemPowerShellPath()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, powershellPath, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script).Run(); err != nil {
		return errors.New("write Start Menu shortcut")
	}
	if _, err := os.Stat(commonShortcutPath); err != nil {
		return errors.New("verify Start Menu shortcut")
	}
	return nil
}

func (platform *WindowsPlatform) ShowError(err error) {
	if err == nil {
		return
	}
	_, _ = showMessageBox(err.Error(), "Mobile Egress Setup", messageBoxIconError|messageBoxTopmost)
}

type shellExecuteInfo struct {
	Size       uint32
	Mask       uint32
	Window     windows.Handle
	Verb       *uint16
	File       *uint16
	Parameters *uint16
	Directory  *uint16
	Show       int32
	Instance   windows.Handle
	IDList     unsafe.Pointer
	Class      *uint16
	ClassKey   windows.Handle
	HotKey     uint32
	Icon       windows.Handle
	Process    windows.Handle
}

type authenticodeResult struct {
	Status            string `json:"status"`
	Thumbprint        string `json:"thumbprint"`
	CertificateSHA256 string `json:"certificateSha256"`
	CertificateBase64 string `json:"certificateBase64"`
	Timestamped       bool   `json:"timestamped"`
}

func validateAuthenticodeResult(raw []byte, identity Identity) error {
	var result authenticodeResult
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return errors.New("Windows Authenticode result is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("Windows Authenticode result is invalid")
	}
	certificateDER, err := base64.StdEncoding.DecodeString(result.CertificateBase64)
	if err != nil || !bytes.Equal(certificateDER, identity.DER) {
		return errors.New("Windows Authenticode signer certificate does not match")
	}
	certificateHash := sha256.Sum256(certificateDER)
	if result.Status != "Valid" || !strings.EqualFold(result.Thumbprint, identity.Thumbprint) ||
		!strings.EqualFold(result.CertificateSHA256, hex.EncodeToString(certificateHash[:])) || !result.Timestamped {
		return errors.New("Windows Authenticode signature is not valid, exact, and timestamped")
	}
	return nil
}

func installVerifiedFiles(files []InstallFile, verify func(string) error) error {
	if len(files) == 0 || verify == nil {
		return errors.New("verified install file set is invalid")
	}
	destinationRoot := filepath.Dir(files[0].Destination)
	for _, file := range files {
		if filepath.Dir(file.Destination) != destinationRoot || filepath.Base(file.Source) != filepath.Base(file.Destination) {
			return errors.New("verified install file set is invalid")
		}
	}
	if err := os.MkdirAll(destinationRoot, 0o755); err != nil {
		return err
	}
	stagingRoot, err := os.MkdirTemp(destinationRoot, ".mobile-egress-staging-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stagingRoot)

	type stagedFile struct {
		path        string
		destination string
	}
	staged := make([]stagedFile, 0, len(files))
	for _, file := range files {
		source, err := os.Open(filepath.Clean(file.Source))
		if err != nil {
			return err
		}
		info, err := source.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 512<<20 {
			source.Close()
			return errors.New("signed install source size is invalid")
		}
		stagedPath := filepath.Join(stagingRoot, filepath.Base(file.Destination))
		destination, err := os.OpenFile(stagedPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			source.Close()
			return err
		}
		written, copyErr := io.Copy(destination, source)
		source.Close()
		if copyErr == nil && written != info.Size() {
			copyErr = errors.New("signed install source changed while copying")
		}
		if copyErr == nil {
			copyErr = destination.Sync()
		}
		if closeErr := destination.Close(); copyErr == nil {
			copyErr = closeErr
		}
		if copyErr != nil {
			return copyErr
		}
		staged = append(staged, stagedFile{path: stagedPath, destination: file.Destination})
	}
	for _, file := range staged {
		if err := verify(file.path); err != nil {
			return err
		}
	}
	for _, file := range staged {
		if err := windows.Rename(file.path, file.destination); err != nil {
			return err
		}
	}
	return nil
}

func ensureCertificateInMachineStore(storeName string, identity Identity) (bool, error) {
	store, err := openMachineStore(storeName)
	if err != nil {
		return false, err
	}
	defer windows.CertCloseStore(store, 0)
	existing, err := findCertificate(store, identity)
	if err != nil {
		return false, err
	}
	if existing != nil {
		defer windows.CertFreeCertificateContext(existing)
		if !certificateContextMatches(existing, identity.DER) {
			return false, errors.New("certificate store contains a conflicting publisher thumbprint")
		}
		return false, nil
	}
	context, err := windows.CertCreateCertificateContext(windows.X509_ASN_ENCODING, &identity.DER[0], uint32(len(identity.DER)))
	if err != nil {
		return false, err
	}
	defer windows.CertFreeCertificateContext(context)
	if err := windows.CertAddCertificateContextToStore(store, context, windows.CERT_STORE_ADD_NEW, nil); err != nil {
		return false, err
	}
	return true, nil
}

func removeExactCertificateFromMachineStore(storeName string, identity Identity) error {
	store, err := openMachineStore(storeName)
	if err != nil {
		return err
	}
	defer windows.CertCloseStore(store, 0)
	context, err := findCertificate(store, identity)
	if err != nil || context == nil {
		return err
	}
	if !certificateContextMatches(context, identity.DER) {
		windows.CertFreeCertificateContext(context)
		return errors.New("refusing to remove a non-matching certificate")
	}
	return windows.CertDeleteCertificateFromStore(context)
}

func openMachineStore(storeName string) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(storeName)
	if err != nil {
		return 0, err
	}
	return windows.CertOpenStore(
		windows.CERT_STORE_PROV_SYSTEM,
		0,
		0,
		windows.CERT_SYSTEM_STORE_LOCAL_MACHINE|windows.CERT_STORE_OPEN_EXISTING_FLAG,
		uintptr(unsafe.Pointer(name)),
	)
}

func findCertificate(store windows.Handle, identity Identity) (*windows.CertContext, error) {
	thumbprint := sha1.Sum(identity.DER)
	blob := windows.CryptHashBlob{Size: uint32(len(thumbprint)), Data: &thumbprint[0]}
	context, err := windows.CertFindCertificateInStore(
		store,
		windows.X509_ASN_ENCODING|windows.PKCS_7_ASN_ENCODING,
		0,
		windows.CERT_FIND_SHA1_HASH,
		unsafe.Pointer(&blob),
		nil,
	)
	if err != nil {
		if errors.Is(err, syscall.Errno(windows.CRYPT_E_NOT_FOUND)) {
			return nil, nil
		}
		return nil, err
	}
	return context, nil
}

func certificateContextMatches(context *windows.CertContext, der []byte) bool {
	if context == nil || context.EncodedCert == nil || context.Length != uint32(len(der)) {
		return false
	}
	return bytes.Equal(unsafe.Slice(context.EncodedCert, context.Length), der)
}

func systemPowerShellPath() (string, error) {
	windowsDirectory, err := windows.GetSystemWindowsDirectory()
	if err != nil {
		return "", err
	}
	path := filepath.Join(windowsDirectory, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if _, err := os.Stat(path); err != nil {
		return "", errors.New("system Windows PowerShell is unavailable")
	}
	return path, nil
}

func showMessageBox(message, caption string, flags uint32) (uintptr, error) {
	messagePointer, err := windows.UTF16PtrFromString(message)
	if err != nil {
		return 0, err
	}
	captionPointer, err := windows.UTF16PtrFromString(caption)
	if err != nil {
		return 0, err
	}
	result, _, callErr := messageBoxW.Call(0, uintptr(unsafe.Pointer(messagePointer)), uintptr(unsafe.Pointer(captionPointer)), uintptr(flags))
	if result == 0 && callErr != nil && callErr != syscall.Errno(0) {
		return 0, callErr
	}
	return result, nil
}
