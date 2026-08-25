package archive

import (
	"archive/tar"
	"crypto/aes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ahmedYasserM/qo/pkg/logger"
	"gopkg.in/yaml.v3"
)

type ValidatorType string

const (
	ValidatorFlag           ValidatorType = "flag"
	ValidatorProcessDead    ValidatorType = "process_dead"
	ValidatorProcessRunning ValidatorType = "process_running"
	ValidatorFileExists     ValidatorType = "file_exists"
	ValidatorFileNotExists  ValidatorType = "file_not_exists"
	ValidatorFileContains   ValidatorType = "file_contains"
	ValidatorFilePerms      ValidatorType = "file_permissions"
)

type Validator struct {
	Type  ValidatorType `yaml:"type" json:"type"`
	Value string        `yaml:"value,omitempty" json:"value,omitempty"`
	Path  string        `yaml:"path,omitempty" json:"path,omitempty"`
	Name  string        `yaml:"name,omitempty" json:"name,omitempty"`
	Mode  string        `yaml:"mode,omitempty" json:"mode,omitempty"`
}

type ChallengeLevel struct {
	ID        int        `yaml:"id" json:"id"`
	Title     string     `yaml:"title" json:"title"`
	Question  string     `yaml:"question" json:"question"`
	Hint      string     `yaml:"hint,omitempty" json:"hint,omitempty"`
	Validator *Validator `yaml:"validator,omitempty" json:"validator,omitempty"`
}

type ChallengeMetadata struct {
	Title       string            `yaml:"title,omitempty" json:"title,omitempty"`
	Difficulty  string            `yaml:"difficulty,omitempty" json:"difficulty,omitempty"`
	Story       string            `yaml:"story,omitempty" json:"story,omitempty"`
	Question    string            `yaml:"question,omitempty" json:"question,omitempty"`
	Levels      []ChallengeLevel  `yaml:"levels,omitempty" json:"levels,omitempty"`
	DefaultHint string            `yaml:"default_hint,omitempty" json:"default_hint,omitempty"`
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
			normalizeMeta(&meta)
			return &meta, nil
		}
	}

	return nil, fmt.Errorf("meta.yaml not found in archive")
}

func normalizeMeta(meta *ChallengeMetadata) {
	if len(meta.Levels) == 0 && meta.Question != "" {
		meta.Levels = []ChallengeLevel{{
			ID:       1,
			Title:    meta.Title,
			Question: meta.Question,
			Hint:     meta.DefaultHint,
		}}
	}
	for i := range meta.Levels {
		if meta.Levels[i].ID == 0 {
			meta.Levels[i].ID = i + 1
		}
		if meta.Levels[i].Question == "" {
			meta.Levels[i].Question = meta.Question
		}
		if meta.Levels[i].Hint == "" && meta.DefaultHint != "" {
			meta.Levels[i].Hint = meta.DefaultHint
		}
	}
}

func DiscoverLevelsFromRootfs(rootfsPath string) ([]ChallengeLevel, error) {
	tmpDir := filepath.Join(rootfsPath, "rootfs", "root", "challenges")
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return nil, err
	}

	var levels []ChallengeLevel
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		var id int
		if _, err := fmt.Sscanf(name, "level%d", &id); err != nil || id <= 0 {
			if _, err := fmt.Sscanf(name, "Level-%d", &id); err != nil || id <= 0 {
				if _, err := fmt.Sscanf(name, "Level%d", &id); err != nil || id <= 0 {
					if _, err := fmt.Sscanf(name, "level-%d", &id); err != nil || id <= 0 {
						continue
					}
				}
			}
		}

		level := ChallengeLevel{ID: id, Title: name}

		qPath := filepath.Join(tmpDir, name, "question.txt")
		if data, err := os.ReadFile(qPath); err == nil {
			level.Question = string(data)
		}

		hPath := filepath.Join(tmpDir, name, "hint.txt")
		if data, err := os.ReadFile(hPath); err == nil {
			level.Hint = string(data)
		}

		levels = append(levels, level)
	}

	if len(levels) == 0 {
		return nil, fmt.Errorf("no level directories found in %s", tmpDir)
	}

	return levels, nil
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
