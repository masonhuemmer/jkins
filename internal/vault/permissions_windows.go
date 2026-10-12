package vault

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func protectExisting(s Store) error {
	for _, path := range []string{s.Dir, filepath.Join(s.Dir, "keys"), s.vaultPath(), s.identityPath()} {
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		if err := restrictAccess(path, false); err != nil {
			return err
		}
	}
	return nil
}

func privateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	return restrictAccess(path, true)
}

func privateFile(file *os.File) error { return restrictAccess(file.Name(), false) }

func restrictAccess(path string, dir bool) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	inherit := ""
	if dir {
		inherit = "OICI"
	}
	sd, err := windows.SecurityDescriptorFromString(fmt.Sprintf("D:P(A;%s;FA;;;%s)(A;%s;FA;;;SY)", inherit, user.User.Sid.String(), inherit))
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
