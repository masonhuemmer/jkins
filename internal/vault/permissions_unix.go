//go:build !windows

package vault

import "os"

func protectExisting(Store) error { return nil }

func privateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	return os.Chmod(path, 0700)
}

func privateFile(file *os.File) error { return file.Chmod(0600) }
