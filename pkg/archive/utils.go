package archive

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/pbkdf2"
)

// Generate a 32 byte key from a password entered by the user
func DeriveKey(password string, salt []byte) []byte {
	return pbkdf2.Key([]byte(password), salt, 100_000, 32, sha256.New)
}

func IsValidFolderStructure(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}

	allowedRootFiles := map[string]bool{"meta.yaml": true}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !allowedRootFiles[entry.Name()] {
			return fmt.Errorf("File %q found in root directory; only subdirectories and meta.yaml are allowed", entry.Name())
		}
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		subDirPath := filepath.Join(root, entry.Name())
		checkFilePath := filepath.Join(subDirPath, "check.sh")

		info, err := os.Stat(checkFilePath)
		if os.IsNotExist(err) {
			return fmt.Errorf("%q is missing 'check.sh'", subDirPath)
		}

		mode := info.Mode()
		if !mode.IsRegular() || mode&0111 == 0 {
			return fmt.Errorf("%q file is not executable", checkFilePath)
		}
	}

	return nil
}
