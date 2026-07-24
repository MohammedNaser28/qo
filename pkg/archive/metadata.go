package archive

import (
	"archive/tar"
	"crypto/aes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ahmedYasserM/qo/pkg/logger"
	"gopkg.in/yaml.v3"
)

type ChallengeMetadata struct {
	Title      string `yaml:"title" json:"title"`
	Difficulty string `yaml:"difficulty" json:"difficulty"`
	Question   string `yaml:"question" json:"question"`
}

func DecryptMetadata(encryptedFile, password string) (*ChallengeMetadata, error) {
	file, err := os.Open(encryptedFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	salt := make([]byte, 16)
	if _, err := io.ReadFull(file, salt); err != nil {
		return nil, fmt.Errorf("read salt: %w", err)
	}

	key := DeriveKey(password, salt)

	nonce := make([]byte, aes.BlockSize)
	if _, err := io.ReadFull(file, nonce); err != nil {
		return nil, fmt.Errorf("read nonce: %w", err)
	}

	decryptReader, err := newStreamDecryptReader(file, key, nonce)
	if err != nil {
		return nil, err
	}

	tr := tar.NewReader(decryptReader)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		if isMetaFile(header.Name) {
			var meta ChallengeMetadata
			if err := yaml.NewDecoder(tr).Decode(&meta); err != nil {
				return nil, fmt.Errorf("parse meta.yaml: %w", err)
			}
			return &meta, nil
		}
	}

	return nil, fmt.Errorf("meta.yaml not found in archive")
}

func isMetaFile(name string) bool {
	if idx := len(name) - 1; idx >= 0 && (name[idx] == '/' || name[idx] == '\\') {
		return false
	}
	for {
		idx := strings.LastIndexAny(name, "/\\")
		if idx >= 0 {
			base := name[idx+1:]
			if base == "meta.yaml" {
				return true
			}
			name = name[:idx]
		} else {
			return name == "meta.yaml"
		}
	}
}

func MetadataToJSON(meta *ChallengeMetadata) ([]byte, error) {
	out, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func PrintMetadata(meta *ChallengeMetadata) {
	data, err := MetadataToJSON(meta)
	if err != nil {
		logger.Error(err)
		return
	}
	fmt.Println(string(data))
}
