package sandbox

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/ahmedYasserM/qo/pkg/logger"
	"golang.org/x/sys/unix"
)

//go:embed rootfs.tar.gz
var embeddedRootfs []byte

const sessionsDir = "/tmp/qo-sessions"
const defaultUser string = "ahmed"

func GenerateSessionPath(studentID string) (string, error) {
	rand.Seed(time.Now().UnixNano())
	suffix := fmt.Sprintf("%04x", rand.Int31())
	sessionID := fmt.Sprintf("%s-%s", studentID, suffix)
	sessionPath := filepath.Join(sessionsDir, sessionID)
	return sessionPath, nil
}

const maxConcurrentSessions = 8

func checkConcurrencyCap() error {
	lockFile := filepath.Join("/tmp", "qo-sessions.lock")
	file, err := os.OpenFile(lockFile, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("failed to open concurrency lock: %w", err)
	}
	defer file.Close()

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("concurrent sessions limit reached")
	}

	countBytes, err := os.ReadFile(lockFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read session count: %w", err)
	}

	count := 0
	if len(countBytes) > 0 {
		count, err = strconv.Atoi(string(countBytes))
		if err != nil {
			return fmt.Errorf("invalid session count in lock file: %w", err)
		}
	}

	if count >= maxConcurrentSessions {
		return fmt.Errorf("concurrent sessions limit reached")
	}

	count++
	if err := os.WriteFile(lockFile, []byte(strconv.Itoa(count)), 0644); err != nil {
		return fmt.Errorf("failed to write session count: %w", err)
	}

	return nil
}

func releaseConcurrencyCap() {
	lockFile := filepath.Join("/tmp", "qo-sessions.lock")
	file, err := os.OpenFile(lockFile, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		logger.Warn(fmt.Sprintf("Failed to open concurrency lock for release: %v", err))
		return
	}
	defer file.Close()

	syscall.Flock(int(file.Fd()), syscall.LOCK_UN)

	countBytes, err := os.ReadFile(lockFile)
	if err == nil && len(countBytes) > 0 {
		sessionCount, err := strconv.Atoi(string(countBytes))
		if err == nil && sessionCount > 0 {
			sessionCount--
			if err := os.WriteFile(lockFile, []byte(strconv.Itoa(sessionCount)), 0644); err != nil {
				logger.Warn(fmt.Sprintf("Failed to decrement session count: %v", err))
			}
		}
	}

	sessionCount, _ := strconv.Atoi(string(countBytes))
	if sessionCount <= 0 {
		os.Remove(lockFile)
	}
}

func setupCgroupV2(sessionID string, pid int) error {
	cgroupPath := filepath.Join("/sys/fs/cgroup", "qo-sessions", sessionID)

	parentPath := filepath.Dir(cgroupPath)
	if err := os.MkdirAll(parentPath, 0755); err != nil {
		return fmt.Errorf("failed to create parent cgroup: %w", err)
	}
	for _, ctrl := range []string{"memory", "pids", "cpu"} {
		_ = os.WriteFile(filepath.Join(parentPath, "cgroup.subtree_control"),
			[]byte("+"+ctrl), 0644)
	}

	if err := os.Mkdir(cgroupPath, 0755); err != nil {
		return fmt.Errorf("failed to create cgroup: %w", err)
	}

	if err := os.WriteFile(filepath.Join(cgroupPath, "memory.max"), []byte("536870912"), 0644); err != nil {
		return fmt.Errorf("failed to set memory limit: %w", err)
	}

	if err := os.WriteFile(filepath.Join(cgroupPath, "pids.max"), []byte("200"), 0644); err != nil {
		return fmt.Errorf("failed to set PIDs limit: %w", err)
	}

	if err := os.WriteFile(filepath.Join(cgroupPath, "cpu.max"), []byte("1000000 1000000"), 0644); err != nil {
		return fmt.Errorf("failed to set CPU limit: %w", err)
	}

	if err := os.WriteFile(filepath.Join(cgroupPath, "cgroup.procs"), []byte(strconv.Itoa(pid)), 0644); err != nil {
		return fmt.Errorf("failed to move process into cgroup: %w", err)
	}

	return nil
}

func cleanupSession(rootfsPath string, sessionID string) {
	rootfsContent := filepath.Join(rootfsPath, "rootfs")

	if err := syscall.Unmount(filepath.Join(rootfsContent, "dev", "pts"), 0); err != nil && !os.IsNotExist(err) {
		logger.Warn(fmt.Sprintf("Failed to unmount devpts: %v", err))
	}
	if err := syscall.Unmount(filepath.Join(rootfsContent, "proc"), 0); err != nil && !os.IsNotExist(err) {
		logger.Warn(fmt.Sprintf("Failed to unmount /proc: %v", err))
	}

	if err := os.RemoveAll(rootfsPath); err != nil {
		logger.Warn(fmt.Sprintf("Failed to remove rootfs directory: %v", err))
	}

	cgroupPath := filepath.Join("/sys/fs/cgroup", "qo-sessions", sessionID)
	if err := os.RemoveAll(cgroupPath); err != nil && !os.IsNotExist(err) {
		logger.Warn(fmt.Sprintf("Failed to remove cgroup: %v", err))
	}

	logger.Info("Session cleaned up successfully")
}

// PathExists checks if a file or directory exists.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

// ExtractRootfs extracts the tar-archived rootfs folder in /tmp
func ExtractRootfs(rootfsPath string) error {
	if pathExists(rootfsPath) {
		rootfsContent := filepath.Join(rootfsPath, "rootfs")
		_ = syscall.Unmount(filepath.Join(rootfsContent, "dev", "pts"), syscall.MNT_FORCE)
		_ = syscall.Unmount(filepath.Join(rootfsContent, "proc"), syscall.MNT_FORCE)

		if err := os.RemoveAll(rootfsPath); err != nil {
			return err
		}
	}

	gzReader, err := gzip.NewReader(io.NopCloser(bytes.NewReader(embeddedRootfs)))
	if err != nil {
		return err
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break // done
		}
		if err != nil {
			return err
		}

		destPath := filepath.Join(rootfsPath, header.Name)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(destPath, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
				return err
			}

			outFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(outFile, tarReader); err != nil {
				outFile.Close()
				return err
			}
			outFile.Close()

		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
				return err
			}

			if err := os.Symlink(header.Linkname, destPath); err != nil {
				return err
			}
		case tar.TypeChar:
			if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
				return err
			}
			dev := int(unix.Mkdev(uint32(header.Devmajor), uint32(header.Devminor)))
			if err := syscall.Mknod(destPath, syscall.S_IFCHR|uint32(header.Mode), dev); err != nil {
				return err
			}
		}
	}
	return nil
}

func StartSandBox(rootfsPath string, duration time.Duration) error {

	if len(os.Args) > 0 && os.Args[0] == "init" {
		releaseConcurrencyCap()
		chrootPath := filepath.Join(rootfsPath, "rootfs")
		if err := syscall.Chroot(chrootPath); err != nil {
			return fmt.Errorf("chroot %s: %w", chrootPath, err)
		}

		if err := os.MkdirAll("/proc", 0555); err != nil {
			return fmt.Errorf("mkdir /proc: %w", err)
		}

		if err := os.MkdirAll("/dev/pts", 0755); err != nil {
			return fmt.Errorf("mkdir /dev/pts: %w", err)
		}

		if err := syscall.Mount("proc", "/proc", "proc", 0, ""); err != nil {
			return fmt.Errorf("mount proc: %w", err)
		}

		if err := syscall.Mount("devpts", "/dev/pts", "devpts", 0, "newinstance,ptmxmode=0666,mode=0620"); err != nil {
			return fmt.Errorf("mount devpts: %w", err)
		}
		if err := os.Remove("/dev/ptmx"); err != nil {
			return fmt.Errorf("remove /dev/ptmx: %w", err)
		}
		if err := os.Symlink("pts/ptmx", "/dev/ptmx"); err != nil {
			return fmt.Errorf("symlink /dev/ptmx: %w", err)
		}

		if err := os.Chdir("/tmp"); err != nil {
			return fmt.Errorf("chdir /tmp: %w", err)
		}

		logger.Info("You are now inside the isolated enviornemnt.")

		cmd := exec.Command("/bin/bash", "-i")
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		return cmd.Run()
	}

	if err := checkConcurrencyCap(); err != nil {
		return err
	}

	cmd := exec.Command("/proc/self/exe")
	cmd.Args = []string{"init", rootfsPath}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS | syscall.CLONE_NEWNET,
		Setpgid:    true,
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start child: %w", err)
	}

	sessionID := filepath.Base(rootfsPath)
	if err := setupCgroupV2(sessionID, cmd.Process.Pid); err != nil {
		logger.Warn(fmt.Sprintf("failed to setup cgroup v2: %v", err))
	}

	if duration > 0 {
		go func() {
			time.Sleep(duration)
			logger.Warn("Duration elapsed, terminating session")
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			time.Sleep(5 * time.Second)
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}()
	}

	if err := cmd.Wait(); err != nil {
		logger.Warn(fmt.Sprintf("Session exited with error: %v", err))
	}

	cleanupSession(rootfsPath, sessionID)

	return nil
}
