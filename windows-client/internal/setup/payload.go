package setup

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
)

var payloadNames = []string{ControllerExecutableName, AdminExecutableName, RelayExecutableName, ClientExecutableName, ManifestName, PublicCertificateName, PublicIdentityRecordName}

// Validate the whole archive before writing anything. Only fixed, flat release
// files are accepted, with bounded expansion and exclusive creation.
func extractPayload(data []byte, directory string) error {
	const maximumPayloadSize = 512 << 20
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return errors.New("embedded setup payload is invalid")
	}
	if len(r.File) != len(payloadNames) {
		return errors.New("embedded setup payload file set is invalid")
	}
	remaining := make(map[string]bool, len(payloadNames))
	for _, name := range payloadNames {
		remaining[name] = true
	}
	var total uint64
	for _, file := range r.File {
		if !remaining[file.Name] || !file.Mode().IsRegular() || file.UncompressedSize64 == 0 || file.UncompressedSize64 > maximumPayloadSize {
			return errors.New("embedded setup payload entry is invalid")
		}
		delete(remaining, file.Name)
		total += file.UncompressedSize64
		if total > maximumPayloadSize {
			return errors.New("embedded setup payload exceeds size limit")
		}
	}
	for _, file := range r.File {
		if err := extractPayloadFile(file, filepath.Join(directory, file.Name)); err != nil {
			return errors.New("extract embedded setup payload")
		}
	}
	return nil
}

func extractPayloadFile(entry *zip.File, destination string) error {
	source, err := entry.Open()
	if err != nil {
		return err
	}
	defer source.Close()
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	count, copyErr := io.Copy(file, io.LimitReader(source, int64(entry.UncompressedSize64)+1))
	closeErr := file.Close()
	if count != int64(entry.UncompressedSize64) {
		return errors.New("payload size mismatch")
	}
	return errors.Join(copyErr, closeErr)
}
