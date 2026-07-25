package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ahmedYasserM/qo/pkg/logger"
)

type ChallengeLevel struct {
	ID       int    `json:"id"`
	Title    string `json:"title"`
	Question string `json:"question"`
	Hint     string `json:"hint,omitempty"`
}

func discoverLevels(rootfsPath string) ([]ChallengeLevel, error) {
	tmpDir := filepath.Join(rootfsPath, "rootfs", "tmp")

	for attempt := 0; attempt < 20; attempt++ {
		entries, err := os.ReadDir(tmpDir)
		if err == nil {
			var levels []ChallengeLevel
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				name := e.Name()
				var id int
				if _, err := fmt.Sscanf(name, "level%d", &id); err != nil || id <= 0 {
					continue
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

			if len(levels) > 0 {
				return levels, nil
			}
		}

		time.Sleep(250 * time.Millisecond)
	}

	return nil, fmt.Errorf("no level directories found in %s", tmpDir)
}

func checkScript(rootfsPath string, levelID int) (bool, error) {
	scriptPath := filepath.Join(rootfsPath, "rootfs", "tmp", fmt.Sprintf("level%d", levelID), "check.sh")
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return false, nil
	}
	cmd := exec.Command("/bin/bash", scriptPath)
	cmd.Dir = filepath.Join(rootfsPath, "rootfs", "tmp", fmt.Sprintf("level%d", levelID))
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode() == 0, nil
		}
		return false, err
	}
	return true, nil
}

func StartChallengeHandler(rootfsPath string) {
	levels, err := discoverLevels(rootfsPath)
	if err != nil || len(levels) == 0 {
		logger.Info("No challenge levels found, challenge handler disabled")
		return
	}

	state := &challengeState{CurrentLevel: 0, Levels: levels}
	logger.Info(fmt.Sprintf("Challenge handler started with %d levels", len(levels)))

	reqFile := filepath.Join(rootfsPath, "rootfs", "tmp", ".qo-challenge-req")
	respFile := filepath.Join(rootfsPath, "rootfs", "tmp", ".qo-challenge-resp")

	for {
		data, err := os.ReadFile(reqFile)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		os.WriteFile(reqFile, []byte{}, 0644)

		action := strings.TrimSpace(string(data))
		if action == "" {
			continue
		}

		var resp []byte

		switch action {
		case "quest":
			current := state.Current()
			resp, _ = json.Marshal(map[string]any{
				"question": current.Question,
				"level":    state.CurrentLevel + 1,
				"total":    state.Total(),
			})
		case "hint":
			current := state.Current()
			hint := current.Hint
			if hint == "" {
				hint = "No hint available."
			}
			resp, _ = json.Marshal(map[string]string{"hint": hint})
		case "go":
			passed, err := checkScript(rootfsPath, state.CurrentLevel+1)
			if err != nil {
				resp, _ = json.Marshal(map[string]any{"passed": false, "message": "Check failed: " + err.Error(), "completed": state.Completed})
			} else if passed {
				advanced := state.Advance()
				msg := "Correct!"
				if advanced {
					msg = fmt.Sprintf("Correct! Advancing to level %d...", state.CurrentLevel+1)
				} else {
					msg = "Correct! You completed all levels!"
				}
				resp, _ = json.Marshal(map[string]any{
					"passed":    true,
					"message":   msg,
					"completed": state.Completed,
					"next": map[string]any{
						"level":    state.CurrentLevel + 1,
						"total":    state.Total(),
						"title":    state.Current().Title,
						"question": state.Current().Question,
						"hint":     state.Current().Hint,
					},
				})
			} else {
				resp, _ = json.Marshal(map[string]any{"passed": false, "message": "Not quite right. Try again!", "completed": state.Completed})
			}
		case "map":
			resp, _ = json.Marshal(state.Status())
		case "status":
			current := state.Current()
			resp, _ = json.Marshal(map[string]any{
				"level":     state.CurrentLevel + 1,
				"total":     state.Total(),
				"title":     current.Title,
				"completed": state.Completed,
			})
		default:
			resp, _ = json.Marshal(map[string]string{"error": "unknown action: " + action})
		}

		os.WriteFile(respFile, append(resp, '\n'), 0644)
	}
}

type challengeState struct {
	CurrentLevel int
	Levels       []ChallengeLevel
	Completed    bool
}

func (c *challengeState) Current() ChallengeLevel {
	if c.CurrentLevel < 0 || c.CurrentLevel >= len(c.Levels) {
		return ChallengeLevel{}
	}
	return c.Levels[c.CurrentLevel]
}

func (c *challengeState) Total() int {
	return len(c.Levels)
}

func (c *challengeState) Advance() bool {
	if c.CurrentLevel+1 >= len(c.Levels) {
		c.Completed = true
		return false
	}
	c.CurrentLevel++
	return true
}

func (c *challengeState) Status() map[string]any {
	levels := make([]map[string]any, len(c.Levels))
	for i, lv := range c.Levels {
		levels[i] = map[string]any{
			"id":        lv.ID,
			"title":     lv.Title,
			"completed": i < c.CurrentLevel,
		}
	}
	return map[string]any{
		"current_level": c.CurrentLevel,
		"total_levels":  len(c.Levels),
		"completed":     c.Completed,
		"current_title": c.Current().Title,
		"levels":        levels,
	}
}
