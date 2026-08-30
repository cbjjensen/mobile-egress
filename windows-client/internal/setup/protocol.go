package setup

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

const (
	InstallOperation      = "install"
	maxExchangeBytes      = 4096
	exchangeDirectoryName = "MobileEgressSetup"
)

var noncePattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Request struct {
	Operation   string `json:"operation"`
	Nonce       string `json:"nonce"`
	SetupSHA256 string `json:"setupSha256"`
}

type Result struct {
	Nonce       string `json:"nonce"`
	SetupSHA256 string `json:"setupSha256,omitempty"`
	Success     bool   `json:"success"`
	Code        string `json:"code,omitempty"`
	Message     string `json:"message"`
}

type Exchange struct {
	Root string
}

func DefaultExchangeRoot() (string, error) {
	cacheRoot, err := os.UserCacheDir()
	if err != nil || cacheRoot == "" {
		return "", errors.New("user setup exchange directory is unavailable")
	}
	return filepath.Join(cacheRoot, exchangeDirectoryName), nil
}

func (exchange Exchange) RequestPath(nonce string) string {
	return filepath.Join(exchange.Root, nonce+".request.json")
}

func (exchange Exchange) ResultPath(nonce string) string {
	return filepath.Join(exchange.Root, nonce+".result.json")
}

func (exchange Exchange) CreateRequest(nonce, setupSHA256 string) error {
	if !noncePattern.MatchString(nonce) || !sha256Pattern.MatchString(setupSHA256) {
		return errors.New("setup request nonce is invalid")
	}
	if exchange.Root == "" {
		return errors.New("setup exchange root is unavailable")
	}
	if err := os.MkdirAll(exchange.Root, 0o700); err != nil {
		return errors.New("create setup exchange directory")
	}
	_ = os.Remove(exchange.ResultPath(nonce))
	return writeExclusiveJSON(exchange.RequestPath(nonce), Request{Operation: InstallOperation, Nonce: nonce, SetupSHA256: setupSHA256})
}

func (exchange Exchange) ConsumeRequest(nonce string) (Request, error) {
	if !noncePattern.MatchString(nonce) {
		return Request{}, errors.New("setup request nonce is invalid")
	}
	path := exchange.RequestPath(nonce)
	var request Request
	if err := readBoundedJSON(path, &request); err != nil {
		return Request{}, fmt.Errorf("read setup request: %w", err)
	}
	if err := os.Remove(path); err != nil {
		return Request{}, errors.New("consume setup request")
	}
	if request.Operation != InstallOperation {
		return Request{}, errors.New("setup request operation is invalid")
	}
	if request.Nonce != nonce {
		return Request{}, errors.New("setup request nonce does not match")
	}
	if !sha256Pattern.MatchString(request.SetupSHA256) {
		return Request{}, errors.New("setup request digest is invalid")
	}
	return request, nil
}

func (exchange Exchange) WriteResult(result Result) error {
	if !noncePattern.MatchString(result.Nonce) || !sha256Pattern.MatchString(result.SetupSHA256) ||
		result.Message == "" || len(result.Message) > 256 || len(result.Code) > 64 {
		return errors.New("setup result is invalid")
	}
	return writeExclusiveJSON(exchange.ResultPath(result.Nonce), result)
}

func (exchange Exchange) ReadResult(nonce string) (Result, error) {
	if !noncePattern.MatchString(nonce) {
		return Result{}, errors.New("setup result nonce is invalid")
	}
	path := exchange.ResultPath(nonce)
	defer os.Remove(path)
	var result Result
	if err := readBoundedJSON(path, &result); err != nil {
		return Result{}, fmt.Errorf("read setup result: %w", err)
	}
	if result.Nonce != nonce || !sha256Pattern.MatchString(result.SetupSHA256) ||
		result.Message == "" || len(result.Message) > 256 || len(result.Code) > 64 {
		return Result{}, errors.New("setup result is invalid")
	}
	return result, nil
}

func FileSHA256(path string) (string, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	defer file.Close()
	return fileSHA256(file)
}

func fileSHA256(file *os.File) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", errors.New("seek setup executable")
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 512<<20 {
		return "", errors.New("setup executable size is invalid")
	}
	hasher := sha256.New()
	written, err := io.Copy(hasher, file)
	if err != nil || written != info.Size() {
		return "", errors.New("setup executable changed while hashing")
	}
	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

func writeExclusiveJSON(path string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) == 0 || len(encoded) > maxExchangeBytes {
		return errors.New("encode setup exchange")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("create setup exchange")
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(encoded); err != nil {
		return errors.New("write setup exchange")
	}
	if err := file.Sync(); err != nil {
		return errors.New("flush setup exchange")
	}
	if err := file.Close(); err != nil {
		return errors.New("close setup exchange")
	}
	ok = true
	return nil
}

func readBoundedJSON(path string, destination any) error {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxExchangeBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > maxExchangeBytes {
		return errors.New("setup exchange size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return errors.New("setup exchange format is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("setup exchange contains trailing data")
	}
	return nil
}
