//go:build !windows

package vault_test

import (
	"os"
	"testing"
)

func assertPrivatePath(t *testing.T, path string, dir bool) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	want := os.FileMode(0600)
	if dir {
		want = 0700
	}
	if info.Mode().Perm() != want {
		t.Errorf("%s mode = %o, want %o", path, info.Mode().Perm(), want)
	}
}
