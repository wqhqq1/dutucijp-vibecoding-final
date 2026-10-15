package auth

import (
	"golang.org/x/sys/windows"
	"os/user"
)

// Replace the DACL, including pre-existing explicit ACEs. Chmod or disabling
// inheritance alone does not restrict a previously shared Windows directory.
func privateACL(path string, directory bool) error {
	u, err := user.Current()
	if err != nil {
		return err
	}
	inherit := ""
	if directory {
		inherit = "OICI"
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;" + inherit + ";FA;;;" + u.Uid + ")")
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}
