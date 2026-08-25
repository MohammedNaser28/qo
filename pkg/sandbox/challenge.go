package sandbox

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed logo.txt
var logoContent string

const (
	ansiCyan   = "\033[96m"
	ansiYellow = "\033[33m"
	ansiGreen  = "\033[32m"
	ansiRed    = "\033[31m"
	ansiBold   = "\033[1m"
	ansiReset  = "\033[0m"
)

type ChallengeLevel struct {
	ID       int    `json:"id"`
	Title    string `json:"title"`
	Question string `json:"question"`
	Hint     string `json:"hint,omitempty"`
}

func discoverLevels(rootfsPath string) ([]ChallengeLevel, error) {
	tmpDir := filepath.Join(rootfsPath, "rootfs", "root", "challenges")

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

func checkScript(rootfsPath string, levelID int, stdinInput string) (bool, error) {
	scriptPath := filepath.Join(rootfsPath, "rootfs", "root", "challenges", fmt.Sprintf("level%d", levelID), "check.sh")
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return false, nil
	}
	cmd := exec.Command("/bin/bash", scriptPath)
	cmd.Dir = filepath.Join(rootfsPath, "rootfs", "root", "challenges", fmt.Sprintf("level%d", levelID))
	if stdinInput != "" {
		cmd.Stdin = strings.NewReader(stdinInput + "\n")
	}
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode() == 0, nil
		}
		return false, err
	}
	return true, nil
}

func StartChallengeHandler(rootfsPath string) {
	defer func() {
		if r := recover(); r != nil {
			debugPath := filepath.Join(rootfsPath, "rootfs", "tmp", ".qo-challenge-debug")
			msg := fmt.Sprintf("PANIC: %v\n", r)
			os.WriteFile(debugPath, []byte(msg), 0644)
		}
	}()

	levels, err := discoverLevels(rootfsPath)
	if err != nil || len(levels) == 0 {
		return
	}

	state := &challengeState{CurrentLevel: 0, Levels: levels}

	reqFile := filepath.Join(rootfsPath, "rootfs", "tmp", ".qo-challenge-req")
	respFile := filepath.Join(rootfsPath, "rootfs", "tmp", ".qo-challenge-resp")
	debugFile := filepath.Join(rootfsPath, "rootfs", "tmp", ".qo-challenge-debug")

	os.WriteFile(debugFile, []byte(fmt.Sprintf("handler: started pid=%d\n", os.Getpid())), 0644)

	logoFile := filepath.Join(rootfsPath, "rootfs", "tmp", ".qo-logo")
	os.WriteFile(logoFile, []byte(logoContent), 0644)

	var tick int
	for {
		data, err := os.ReadFile(reqFile)
		if err != nil {
			tick++
			if tick%25 == 0 {
				os.WriteFile(debugFile, []byte(fmt.Sprintf("handler: waiting tick=%d\n", tick)), 0644)
			}
			time.Sleep(200 * time.Millisecond)
			continue
		}

		os.WriteFile(debugFile, []byte(fmt.Sprintf("handler: read raw=%q\n", string(data))), 0644)
		os.WriteFile(reqFile, []byte{}, 0644)

		action := strings.TrimSpace(string(data))
		if action == "" {
			os.WriteFile(debugFile, []byte("handler: empty action\n"), 0644)
			continue
		}

		os.WriteFile(debugFile, []byte(fmt.Sprintf("handler: action=%s\n", action)), 0644)

		var resp string
		var goAnswer string
		if strings.HasPrefix(action, "go") && (len(action) == 2 || action[2] == ':') {
			if len(action) > 3 {
				goAnswer = action[3:]
			}
			action = "go"
		}

		switch action {
		case "quest":
			current := state.Current()
			resp = fmt.Sprintf("%s━━━ Level %d/%d ━━━%s\n%s",
				ansiCyan, state.CurrentLevel+1, state.Total(), ansiReset,
				current.Question)
		case "hint":
			current := state.Current()
			hint := current.Hint
			if hint == "" {
				hint = "No hint available."
			}
			resp = fmt.Sprintf("%s💡 Hint: %s%s", ansiYellow, hint, ansiReset)
		case "go":
			passed, err := checkScript(rootfsPath, state.CurrentLevel+1, goAnswer)
			if err != nil {
				resp = fmt.Sprintf("%s❌ Check failed: %s%s", ansiRed, err.Error(), ansiReset)
			} else if passed {
				advanced := state.Advance()
				if advanced {
					resp = fmt.Sprintf("%s✅ Correct! Advancing to level %d...%s", ansiGreen, state.CurrentLevel+1, ansiReset)
				} else {
					resp = fmt.Sprintf("%s🎉 Correct! You completed all levels!%s", ansiGreen, ansiReset)
				}
			} else {
				resp = fmt.Sprintf("%s❌ Not quite right. Try again!%s", ansiRed, ansiReset)
			}
		case "map":
			s := state.Status()
			resp = fmt.Sprintf("%s━━━ Progress ━━━%s\n", ansiCyan, ansiReset)
			for _, l := range s["levels"].([]map[string]any) {
				icon := "⬜"
				title := l["title"].(string)
				if l["completed"].(bool) {
					icon = "✅"
				}
				if l["id"].(int) == s["current_level"].(int)+1 {
					icon = "📍"
				}
				resp += fmt.Sprintf("  %s %s %s\n", icon, title, ansiReset)
			}
			resp += fmt.Sprintf("\n%s%d/%d levels completed%s", ansiBold, s["current_level"].(int), s["total_levels"].(int), ansiReset)
		case "status":
			current := state.Current()
			resp = fmt.Sprintf("%sLevel %d/%d%s  %s%s%s",
				ansiCyan, state.CurrentLevel+1, state.Total(), ansiReset,
				ansiBold, current.Title, ansiReset)
		case "logo":
			resp = logoContent
		case "help":
			resp = fmt.Sprintf(`%sAvailable commands:%s
  %squest%s   — Show the current challenge question
  %shint%s    — Get a hint for the current level
  %sgo%s      — Check your solution and advance
  %smap%s     — View progress across all levels
  %sstatus%s  — Show current level info
  %slogo%s    — Print the ASCII logo`,
				ansiBold, ansiReset,
				ansiGreen, ansiReset,
				ansiYellow, ansiReset,
				ansiCyan, ansiReset,
				ansiBold, ansiReset,
				ansiBold, ansiReset,
				ansiBold, ansiReset)
		default:
			resp = fmt.Sprintf("%sunknown action: %s%s", ansiRed, action, ansiReset)
		}

		os.WriteFile(debugFile, []byte(fmt.Sprintf("handler: wrote resp for %s\n", action)), 0644)
		os.WriteFile(respFile, append([]byte(resp+ansiReset), '\n'), 0644)
		os.WriteFile(debugFile, []byte(fmt.Sprintf("handler: done %s\n", action)), 0644)
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
