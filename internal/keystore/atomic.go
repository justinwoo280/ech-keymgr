package keystore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// atomicWrite writes data to path with the given mode, using the
// classic tmp + fsync + rename dance so that concurrent readers
// (e.g. an nginx reload) never observe a partial file.
//
// The temporary file lives in the same directory as the target so
// rename is guaranteed to be atomic on the same filesystem.
//
// On any error after the tmp file is created, the tmp file is
// removed before returning so we never leak `*.tmp.<random>` files.
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if path == "" {
		return errors.New("keystore: empty path in atomicWrite")
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	suffix, err := randomSuffix()
	if err != nil {
		return fmt.Errorf("keystore: tmp suffix: %w", err)
	}
	tmpPath := filepath.Join(dir, "."+base+".tmp."+suffix)

	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("keystore: create tmp: %w", err)
	}
	cleanup := func() {
		_ = os.Remove(tmpPath)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		cleanup()
		return fmt.Errorf("keystore: write tmp: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		cleanup()
		return fmt.Errorf("keystore: fsync tmp: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return fmt.Errorf("keystore: close tmp: %w", err)
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		cleanup()
		return fmt.Errorf("keystore: chmod tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("keystore: rename tmp → final: %w", err)
	}

	// Best-effort directory fsync so the rename hits the inode
	// table on disk. Linux + ext4 needs this for full durability;
	// on platforms / FSes where Sync on a directory isn't supported
	// (notably Windows) we silently ignore the error.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// removeIfExists deletes path, ignoring "no such file" errors. Used
// during rollback paths where it's not interesting whether the file
// was actually present.
func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
