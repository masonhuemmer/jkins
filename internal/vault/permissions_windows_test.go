package vault_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/masonhuemmer/jkins/internal/vault"
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
	for _, principal := range []string{";;;WD)", ";;;BU)", ";;;AU)", ";;;BA)"} {
		if strings.Contains(sddl, principal) {
			t.Errorf("%s grants access to %s: %s", path, principal, sddl)
		}
	}
}

func TestLoadRepairsExistingVaultACL(t *testing.T) {
	store := vault.Store{Dir: t.TempDir()}
	want := vault.Credential{User: "alice", Token: "secret"}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Dir, "keys", "identity.txt")
	sd, err := windows.SecurityDescriptorFromString("D:(A;;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	got, present, err := store.Load()
	if err != nil || !present || got != want {
		t.Fatalf("Load() = %+v, %v, %v", got, present, err)
	}
	assertPrivatePath(t, path, false)
}
