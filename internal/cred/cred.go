package cred

import (
	"fmt"

	"github.com/danieljoos/wincred"
)

const appPrefix = "wintp:"

func targetName(connectionID string) string {
	return appPrefix + connectionID
}

// Set stores a password for the given connection ID in Windows Credential Manager.
func Set(connectionID, password string) error {
	cred := wincred.NewGenericCredential(targetName(connectionID))
	cred.CredentialBlob = []byte(password)
	cred.UserName = connectionID
	if err := cred.Write(); err != nil {
		return fmt.Errorf("write credential: %w", err)
	}
	return nil
}

// Get retrieves the password for the given connection ID.
func Get(connectionID string) (string, error) {
	c, err := wincred.GetGenericCredential(targetName(connectionID))
	if err != nil {
		return "", fmt.Errorf("read credential: %w", err)
	}
	return string(c.CredentialBlob), nil
}

// Delete removes the stored credential for a connection.
func Delete(connectionID string) error {
	c, err := wincred.GetGenericCredential(targetName(connectionID))
	if err != nil {
		// Not found is fine.
		return nil
	}
	if err := c.Delete(); err != nil {
		return fmt.Errorf("delete credential: %w", err)
	}
	return nil
}
