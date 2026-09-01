package service

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

func writeDurableFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	written := false
	defer func() {
		_ = file.Close()
		if !written {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	written = true
	return nil
}

func syncFile(path string) error {
	// Windows requires a writable handle for FlushFileBuffers. Darwin accepts
	// the same O_RDWR handle for fsync, so use one portable durability path.
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	err = directory.Sync()
	if err != nil && runtime.GOOS == "windows" {
		return nil
	}
	return err
}

func syncAdminSetupStage(stageDir string) error {
	for _, name := range []string{caCertFilename, caKeyFilename, relayCertFilename, relayKeyFilename, databaseFilename} {
		if err := syncFile(filepath.Join(stageDir, name)); err != nil {
			return fmt.Errorf("sync relay admin setup file: %w", err)
		}
	}
	if err := syncDirectory(stageDir); err != nil {
		return fmt.Errorf("sync relay admin setup directory: %w", err)
	}
	return nil
}

func removeAdminSetupStage(stageDir string) error {
	if err := validateAdminSetupStageContents(stageDir, false); err != nil {
		return err
	}
	entries, err := os.ReadDir(stageDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.Remove(filepath.Join(stageDir, entry.Name())); err != nil {
			return err
		}
	}
	return os.Remove(stageDir)
}

func validateAdminSetupStageContents(stageDir string, requireComplete bool) error {
	entries, err := os.ReadDir(stageDir)
	if err != nil {
		return err
	}
	allowed := map[string]bool{
		caCertFilename: true, caKeyFilename: true, relayCertFilename: true, relayKeyFilename: true,
		databaseFilename: true, databaseFilename + "-wal": true, databaseFilename + "-shm": true,
	}
	present := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			return ErrAdminStateIncompatible
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0 {
			return ErrAdminStateIncompatible
		}
		present[entry.Name()] = true
	}
	if requireComplete {
		for _, name := range []string{caCertFilename, caKeyFilename, relayCertFilename, relayKeyFilename, databaseFilename} {
			if !present[name] {
				return ErrAdminStateIncompatible
			}
		}
	}
	return nil
}
