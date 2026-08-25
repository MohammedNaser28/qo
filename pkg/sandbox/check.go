package sandbox

// check.go - check.sh execution over a per-session Unix domain socket.
//
// The real check.sh scripts live in the protected ChallengesDir (root-only,
// outside the chroot). Inside the chroot, each level gets a thin, non-secret
// stub (see WriteCheckStubs) that relays a request to this package's check
// server. The server runs in the privileged parent process that launched the
// sandbox session and stays alive for the whole session. On a request it forks
// a short-lived child, chroot()s it into the same rootfs the student is using,
// and pipes the real script's content into bash via stdin - the script text is
// never written anywhere the student's shell can read.
//
// Security note (deliberate deviation from a strict 0600 root socket): the stub
// runs as the dropped sandbox user (uid/gid 1000), and a 0600 root-owned unix
// socket is unreachable from that uid - the only legitimate client. The socket
// is therefore 0660 owned by root:1000: still root-owned, session-scoped, and
// not world-accessible, while remaining reachable by the sandbox user.

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

const (
	checkSocketInChroot = "/tmp/qo-check.sock"
	checkSocketName     = "qo-check.sock"
	checkClientName     = "qo-check"
	checkStubName       = "check.sh"

	setupClientName = "qo-setup"
	resetClientName = "qo-reset"

	checkResponseOK    = 0
	checkResponseFail  = 1
	checkResponseError = 2

	setupResponseOK    = 0
	setupResponseFail  = 1
	setupResponseError = 2
)

var (
	keyTokenRe = regexp.MustCompile(`(?i)\bkey\s*=\s*"([^"]+)"`)

	LeaderboardHook func(studentID, levelKey, flag string)
)

func checkSocketHostPath() string {
	return filepath.Join(Rootfs, "tmp", checkSocketName)
}

func StartCheckServer(studentID string) (net.Listener, error) {
	sock := checkSocketHostPath()
	if err := os.MkdirAll(filepath.Dir(sock), 0755); err != nil {
		return nil, fmt.Errorf("creating socket dir: %w", err)
	}
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return nil, fmt.Errorf("binding check socket: %w", err)
	}
	_ = os.Chown(sock, 0, 1000)
	_ = os.Chmod(sock, 0660)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleCheckConn(conn, studentID)
		}
	}()

	return ln, nil
}

func handleCheckConn(conn net.Conn, studentID string) {
	defer conn.Close()

	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		fmt.Fprintf(conn, "QOCHECK %d\nbad request: %v\n", checkResponseError, err)
		return
	}
	verb := strings.SplitN(strings.TrimRight(line, "\n"), "\t", 2)[0]
	switch verb {
	case "setup", "reset":
		handleSetupConn(conn, line, verb == "reset")
	default:
		handleCheckConnVerb(conn, line, studentID)
	}
}

func handleSetupConn(conn net.Conn, line string, reset bool) {
	verb := "SETUP"
	if reset {
		verb = "RESET"
	}
	parts := strings.Split(strings.TrimRight(line, "\n"), "\t")
	if len(parts) != 4 {
		fmt.Fprintf(conn, "QO%s %d\nbad request\n", verb, setupResponseError)
		return
	}
	uid, errU := strconv.ParseUint(parts[2], 10, 32)
	gid, errG := strconv.ParseUint(parts[3], 10, 32)
	if errU != nil || errG != nil {
		fmt.Fprintf(conn, "QO%s %d\nmalformed uid/gid\n", verb, setupResponseError)
		return
	}
	summary, err := runSetup(parts[1], uint32(uid), uint32(gid), reset)
	if err != nil {
		fmt.Fprintf(conn, "QO%s %d\n%s\n", verb, setupResponseFail, err)
		return
	}
	fmt.Fprintf(conn, "QO%s %d\n%s\n", verb, setupResponseOK, summary)
}

func runSetup(rawKey string, uid, gid uint32, reset bool) (string, error) {
	rawKey = strings.TrimSpace(rawKey)
	if rawKey != "" && rawKey != "*" && rawKey != "all" && !validateLevelKey(rawKey) {
		return "", fmt.Errorf("invalid level %q", rawKey)
	}
	key := normalizeLevelKey(rawKey)
	if key == "" || key == "*" || key == "all" {
		return setupAll(uid, gid)
	}
	if !validateLevelKey(key) {
		return "", fmt.Errorf("invalid level %q", rawKey)
	}
	if _, err := copyLevelToHome(key, uid, gid); err != nil {
		return "", err
	}
	return fmt.Sprintf("level %s ready in ~/%s", rawKey, key), nil
}

func setupAll(uid, gid uint32) (string, error) {
	base := filepath.Join(PristineDir, "challenges")
	entries, err := os.ReadDir(base)
	if err != nil {
		return "", fmt.Errorf("no challenge levels available: %w", err)
	}
	done, failed := 0, 0
	var errs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		key := filepath.Join("challenges", e.Name())
		if !validateLevelKey(key) {
			continue
		}
		if _, err := copyLevelToHome(key, uid, gid); err != nil {
			failed++
			errs = append(errs, fmt.Sprintf("%s: %v", key, err))
			continue
		}
		done++
	}
	if failed > 0 {
		return fmt.Sprintf("set up %d levels, %d failed: %s", done, failed, strings.Join(errs, "; ")),
			fmt.Errorf("%d levels failed", failed)
	}
	return fmt.Sprintf("set up %d levels in ~/challenges", done), nil
}

func copyLevelToHome(levelKey string, uid, gid uint32) (int, error) {
	if !validateLevelKey(levelKey) {
		return 0, fmt.Errorf("invalid level %q", levelKey)
	}
	src := filepath.Join(PristineDir, levelKey)
	if !pathExists(src) {
		return 0, fmt.Errorf("level %q is not available", levelKey)
	}
	home := homeDirFor(uid)
	dst := filepath.Join(Rootfs, "rootfs", home, levelKey)
	if err := os.RemoveAll(dst); err != nil {
		return 0, err
	}
	if err := copyDir(src, dst); err != nil {
		return 0, err
	}
	if os.Getuid() == 0 {
		_ = chownTree(dst, int(uid), int(gid))
	}
	return countFiles(dst), nil
}

func homeDirFor(uid uint32) string {
	if uid == 0 {
		return "/root"
	}
	return "/home/ahmed"
}

func normalizeLevelKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" || key == "*" || key == "all" {
		return key
	}
	if strings.HasPrefix(key, "challenges/") {
		return key
	}
	return "challenges/" + key
}

func chownTree(root string, uid, gid int) error {
	return filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		return os.Chown(p, uid, gid)
	})
}

func countFiles(root string) int {
	n := 0
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			n++
		}
		return nil
	})
	return n
}

func handleCheckConnVerb(conn net.Conn, line, studentID string) {
	req, err := readCheckRequestLine(line)
	if err != nil {
		fmt.Fprintf(conn, "QOCHECK %d\nbad request: %v\n", checkResponseError, err)
		return
	}

	output, code, baseFlag, err := runLevelCheck(req)
	if err != nil {
		fmt.Fprintf(conn, "QOCHECK %d\n%s\n", checkResponseError, err)
		return
	}

	if code == checkResponseOK && baseFlag != "" {
		reqID := studentID
		if req.studentID != "" {
			reqID = req.studentID
		}
		flag := GenerateUniqueFlag(baseFlag, reqID)
		output = presentCheckSuccess(output, baseFlag, flag)
		if LeaderboardHook != nil {
			LeaderboardHook(reqID, req.levelKey, flag)
		}
	} else if code == checkResponseOK {
		fmt.Fprintf(os.Stderr, "[qo] level %q passed but no base flag file (.base_flag/flag.txt/key=) was found\n", req.levelKey)
	}

	fmt.Fprintf(conn, "QOCHECK %d\n%s", code, output)
}

func GenerateUniqueFlag(baseFlag, studentID string) string {
	mac := hmac.New(sha256.New, []byte(baseFlag))
	mac.Write([]byte(studentID))
	res := hex.EncodeToString(mac.Sum(nil))
	if len(res) > 16 {
		return res[:16]
	}
	return res
}

type checkRequest struct {
	levelKey  string
	cwd       string
	uid       uint32
	gid       uint32
	studentID string
	args      []string
}

func readCheckRequestLine(line string) (checkRequest, error) {
	var req checkRequest
	parts := strings.Split(strings.TrimRight(line, "\n"), "\t")
	if len(parts) < 5 || parts[0] != "check" {
		return req, fmt.Errorf("malformed request")
	}
	uid, errU := strconv.ParseUint(parts[3], 10, 32)
	gid, errG := strconv.ParseUint(parts[4], 10, 32)
	if errU != nil || errG != nil {
		return req, fmt.Errorf("malformed uid/gid")
	}
	req.levelKey = parts[1]
	req.cwd = parts[2]
	req.uid = uint32(uid)
	req.gid = uint32(gid)

	rest := parts[5:]
	if len(rest) == 0 {
		return req, nil
	}
	req.studentID = rest[0]
	rest = rest[1:]
	if len(rest) == 0 {
		return req, nil
	}
	n, err := strconv.Atoi(rest[0])
	if err != nil || n < 0 || len(rest)-1 < n {
		return req, fmt.Errorf("malformed arg count")
	}
	req.args = rest[1 : 1+n]
	return req, nil
}

func runLevelCheck(req checkRequest) (string, int, string, error) {
	if !validateLevelKey(req.levelKey) {
		return "", checkResponseError, "", fmt.Errorf("invalid level %q", req.levelKey)
	}
	levelDir := filepath.Join(ChallengesDir, req.levelKey)

	script, err := os.ReadFile(filepath.Join(levelDir, checkStubName))
	if err != nil {
		return "", checkResponseError, "", fmt.Errorf("check script unavailable for %q: %v", req.levelKey, err)
	}
	baseFlag, _ := loadBaseFlag(levelDir)

	cwd := req.cwd
	if cwd == "" || !filepath.IsAbs(cwd) {
		cwd = "/home/ahmed"
	}
	hostCwd := filepath.Join(Rootfs, cwd)
	if !strings.HasPrefix(filepath.Clean(hostCwd), filepath.Clean(Rootfs)) {
		return "", checkResponseError, "", fmt.Errorf("invalid cwd %q", req.cwd)
	}

	home := "/home/ahmed"
	user := "ahmed"
	if req.uid == 0 {
		home = "/root"
		user = "root"
	}

	cmdArgs := []string{"-s", "--"}
	cmdArgs = append(cmdArgs, req.args...)
	cmd := exec.Command("/bin/bash", cmdArgs...)
	cmd.Dir = cwd
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Chroot: Rootfs,
	}
	if req.uid != 0 {
		cmd.SysProcAttr.Credential = &syscall.Credential{
			Uid:    req.uid,
			Gid:    req.gid,
			Groups: []uint32{req.gid},
		}
	}
	cmd.Env = []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=" + home,
		"USER=" + user,
		"LOGNAME=" + user,
		"TERM=xterm",
		"LD_LIBRARY_PATH=/usr/lib:/lib:/lib/x86_64-linux-gnu:/usr/lib/x86_64-linux-gnu:/lib/aarch64-linux-gnu:/usr/lib/aarch64-linux-gnu",
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", checkResponseError, "", err
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		return "", checkResponseError, "", fmt.Errorf("starting check child: %w", err)
	}
	if _, err := io.WriteString(stdin, string(script)); err != nil {
		_ = stdin.Close()
		_ = cmd.Wait()
		return out.String(), checkResponseError, "", err
	}
	_ = stdin.Close()

	code := checkResponseOK
	if err := cmd.Wait(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
			if code != checkResponseFail {
				code = checkResponseFail
			}
		} else {
			code = checkResponseFail
		}
	}
	return out.String(), code, baseFlag, nil
}

func presentCheckSuccess(output, baseFlag, flag string) string {
	out := output
	if baseFlag != "" {
		out = strings.ReplaceAll(out, baseFlag, flag)
	}
	if !strings.Contains(out, flag) {
		out = strings.TrimRight(out, "\n") + "\n\nCongratulations! Your flag is: " + flag + "\n"
	}
	return out
}

func loadBaseFlag(levelDir string) (string, error) {
	for _, name := range []string{".base_flag", "flag.txt"} {
		if b, err := os.ReadFile(filepath.Join(levelDir, name)); err == nil {
			if s := strings.TrimSpace(string(b)); s != "" {
				return s, nil
			}
		}
	}
	if b, err := os.ReadFile(filepath.Join(levelDir, checkStubName)); err == nil {
		if m := keyTokenRe.FindStringSubmatch(string(b)); m != nil {
			return m[1], nil
		}
	}
	return "", fmt.Errorf("no base flag in %s", levelDir)
}

func validateLevelKey(levelKey string) bool {
	if levelKey == "" {
		return false
	}
	if strings.ContainsAny(levelKey, "'\x00\t\n") {
		return false
	}
	if filepath.IsAbs(levelKey) {
		return false
	}
	clean := filepath.Clean(levelKey)
	if clean == "." || clean == ".." {
		return false
	}
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		if part == "." || part == ".." {
			return false
		}
	}
	root := filepath.Clean(ChallengesDir)
	joined := filepath.Join(root, clean)
	if joined != root && !strings.HasPrefix(joined, root+string(filepath.Separator)) {
		return false
	}
	return true
}

func WriteCheckStubs() (int, error) {
	if !pathExists(ChallengesDir) {
		return 0, nil
	}
	var levels []string
	err := filepath.Walk(ChallengesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || info.Name() != checkStubName {
			return nil
		}
		rel, err := filepath.Rel(ChallengesDir, filepath.Dir(path))
		if err != nil {
			return err
		}
		if validateLevelKey(rel) {
			levels = append(levels, rel)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	n := 0
	for _, lvl := range levels {
		stub := filepath.Join(Rootfs, "rootfs", "root", "challenges", lvl, checkStubName)
		if err := os.MkdirAll(filepath.Dir(stub), 0755); err != nil {
			return n, err
		}
		content := fmt.Sprintf("#!/bin/sh\n/bin/%s %s '%s' \"$@\"\nexit $?\n", checkClientName, checkSocketInChroot, lvl)
		if err := os.WriteFile(stub, []byte(content), 0755); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func CopyCheckClient() error {
	return copyClientBin(checkClientName)
}

func CopySetupClients() error {
	if err := copyClientBin(setupClientName); err != nil {
		return err
	}
	reset := filepath.Join(Rootfs, "rootfs", "bin", resetClientName)
	_ = os.Remove(reset)
	return os.Symlink(setupClientName, reset)
}

func copyClientBin(name string) error {
	self, err := os.Readlink("/proc/self/exe")
	if err != nil {
		return fmt.Errorf("resolving self: %w", err)
	}
	dst := filepath.Join(Rootfs, "rootfs", "bin", name)
	_ = os.Remove(dst)
	if err := copyFile(self, dst); err != nil {
		return fmt.Errorf("copying %s: %w", name, err)
	}
	return os.Chmod(dst, 0755)
}

func RunCheckClient(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: qo-check <socket> <level>")
		return checkResponseError
	}
	sock := args[0]
	level := args[1]
	cwd, _ := os.Getwd()

	conn, err := net.Dial("unix", sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "check unavailable: %v\n", err)
		return checkResponseError
	}
	defer conn.Close()

	req := fmt.Sprintf("check\t%s\t%s\t%d\t%d", level, cwd, os.Getuid(), os.Getgid())
	sid := os.Getenv("QO_STUDENT_ID")
	if sid != "" || len(args) > 2 {
		req += "\t" + sid
		req += fmt.Sprintf("\t%d", len(args)-2)
		for _, a := range args[2:] {
			req += "\t" + a
		}
	}
	req += "\n"
	if _, err := io.WriteString(conn, req); err != nil {
		fmt.Fprintf(os.Stderr, "check request failed: %v\n", err)
		return checkResponseError
	}

	br := bufio.NewReader(conn)
	header, err := br.ReadString('\n')
	if err != nil {
		fmt.Fprintf(os.Stderr, "check response failed: %v\n", err)
		return checkResponseError
	}
	var code int
	if _, err := fmt.Sscanf(strings.TrimSpace(header), "QOCHECK %d", &code); err != nil {
		fmt.Fprintf(os.Stderr, "malformed check response: %v\n", err)
		return checkResponseError
	}
	if _, err := io.Copy(os.Stdout, br); err != nil {
		return checkResponseError
	}
	return code
}

// RunLevelCheck runs a level check and returns the output and error.
// This is an exported function for use by the server to run checks
// in a chrooted child, similar to qo2's mechanism.
func RunLevelCheck(levelKey, cwd string, uid, gid uint32, args []string, studentID string) (string, error) {
	req := checkRequest{
		levelKey:  levelKey,
		cwd:       cwd,
		uid:       uid,
		gid:       gid,
		studentID: studentID,
		args:      args,
	}
	output, code, baseFlag, err := runLevelCheck(req)
	if err != nil {
		return "", err
	}
	if code == checkResponseOK && baseFlag != "" {
		flag := GenerateUniqueFlag(baseFlag, studentID)
		output = presentCheckSuccess(output, baseFlag, flag)
		if LeaderboardHook != nil {
			LeaderboardHook(studentID, levelKey, flag)
		}
	} else if code == checkResponseOK {
		fmt.Fprintf(os.Stderr, "[qo] level %q passed but no base flag file (.base_flag/flag.txt/key=) was found\n", levelKey)
	}
	if code != checkResponseOK {
		return "", fmt.Errorf("check failed with exit code %d", code)
	}
	return output, nil
}

func RunSetupClient(args []string) int {
	return runSetupClient(args, "setup")
}

func RunResetClient(args []string) int {
	return runSetupClient(args, "reset")
}

func runSetupClient(args []string, verb string) int {
	if len(args) > 1 {
		fmt.Fprintf(os.Stderr, "usage: %s [level1|level2|...|all]\n", verb)
		return 1
	}
	key := ""
	if len(args) == 1 {
		key = args[0]
	}

	conn, err := net.Dial("unix", checkSocketInChroot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s unavailable: %v\n", verb, err)
		return 1
	}
	defer conn.Close()

	if _, err := fmt.Fprintf(conn, "%s\t%s\t%d\t%d\n", verb, key, os.Getuid(), os.Getgid()); err != nil {
		fmt.Fprintf(os.Stderr, "%s failed: %v\n", verb, err)
		return 1
	}

	br := bufio.NewReader(conn)
	header, err := br.ReadString('\n')
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s failed: %v\n", verb, err)
		return 1
	}
	body, _ := io.ReadAll(br)
	fmt.Print(string(body))
	if strings.HasSuffix(strings.TrimSpace(header), " 0") {
		return 0
	}
	return 1
}
