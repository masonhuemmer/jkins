package vault_test

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func assertPrivatePath(t *testing.T, path string, _ bool) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Errorf("%s inherits its ACL", path)
	}
	sddl := sd.String()
	if !strings.Contains(sddl, "D:P") {
		t.Errorf("%s has unprotected DACL: %s", path, sddl)
	}
	for _, principal := range []string{";;;WD)", ";;;BU)", ";;;AU)"} {
		if strings.Contains(sddl, principal) {
			t.Errorf("%s grants access to %s: %s", path, principal, sddl)
		}
	}
}
