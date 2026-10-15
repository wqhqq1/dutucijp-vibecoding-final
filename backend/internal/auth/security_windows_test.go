package auth

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"os"
	"os/user"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Create a deliberately shared legacy directory/file. This is confined to
// t.TempDir and contains only a random synthetic master key.
func sharedTestDACL(t *testing.T, path string, directory bool) {
	t.Helper()
	u, e := user.Current()
	if e != nil {
		t.Fatal(e)
	}
	inherit := ""
	if directory {
		inherit = "OICI"
	}
	sd, e := windows.SecurityDescriptorFromString("D:P(A;" + inherit + ";FA;;;" + u.Uid + ")(A;" + inherit + ";GR;;;WD)")
	if e != nil {
		t.Fatal(e)
	}
	acl, _, e := sd.DACL()
	if e != nil {
		t.Fatal(e)
	}
	if e = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); e != nil {
		t.Fatal("could not prepare isolated shared ACL:", e)
	}
	actual, e := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		t.Fatal(e)
	}
	actualACL, _, e := actual.DACL()
	if e != nil || actualACL == nil {
		t.Fatal("shared-directory precondition could not be read")
	}
	// Windows expands an inheritable generic-read ACE into effective and
	// inherit-only ACEs; test the actual dangerous grant rather than ACE count.
	worldGrant := false
	for i := uint32(0); i < uint32(actualACL.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if e = windows.GetAce(actualACL, i, &ace); e != nil {
			t.Fatal(e)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if ace.Header.AceType == windows.ACCESS_ALLOWED_ACE_TYPE && sid.IsWellKnown(windows.WinWorldSid) && ace.Header.AceFlags&windows.INHERITED_ACE == 0 && ace.Mask != 0 {
			worldGrant = true
		}
	}
	if !worldGrant {
		t.Fatal("shared-directory precondition lacks actual explicit Everyone grant")
	}
}
func assertPrivateTestDACL(t *testing.T, path string) {
	t.Helper()
	u, e := user.Current()
	if e != nil {
		t.Fatal(e)
	}
	currentSID, e := windows.StringToSid(u.Uid)
	if e != nil {
		t.Fatal(e)
	}
	sd, e := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		t.Fatal(e)
	}
	control, _, e := sd.Control()
	if e != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("private DACL is not protected from inherited permissions")
	}
	acl, _, e := sd.DACL()
	if e != nil || acl == nil || acl.AceCount != 1 {
		t.Fatal("private DACL does not contain exactly one current-user ACE")
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if e = windows.GetAce(acl, 0, &ace); e != nil {
		t.Fatal(e)
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !sid.Equals(currentSID) || uint32(ace.Mask) != 0x001f01ff {
		t.Fatal("private DACL grants a different principal or access level")
	}
}

func TestTC48_WindowsPrivateDACLRemovesLegacyEveryoneAccess(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "legacy-secrets")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	fake := make([]byte, 32)
	if _, e := rand.Read(fake); e != nil {
		t.Fatal(e)
	}
	master := filepath.Join(dir, "master.key")
	if e := os.WriteFile(master, fake, 0600); e != nil {
		t.Fatal(e)
	}
	sharedTestDACL(t, dir, true)
	sharedTestDACL(t, master, false)
	db, e := sql.Open("sqlite", filepath.ToSlash(filepath.Join(t.TempDir(), "acl-isolated.sqlite"))+"?_pragma=busy_timeout(10000)")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = New(db, Config{SecretDir: dir, Origin: "http://localhost:8080"}); e != nil {
		t.Fatal("startup could not restrict legacy explicit ACL:", e)
	}
	assertPrivateTestDACL(t, dir)
	assertPrivateTestDACL(t, master)
	stored, e := os.ReadFile(master)
	if e != nil || !bytes.Equal(fake, stored) {
		t.Fatal("ACL repair altered synthetic existing key contents")
	}
	output := filepath.Join(dir, "initialization-test.json")
	if e = PrivateWrite(output, []byte("{}")); e != nil {
		t.Fatal(e)
	}
	assertPrivateTestDACL(t, output)
	t.Log("actual: legacy directory and synthetic master with explicit Everyone read access were replaced by protected current-user-only DACLs; key bytes retained; new initialization file is private")
}
