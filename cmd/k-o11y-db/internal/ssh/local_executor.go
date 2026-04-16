package ssh

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// LocalExecutor implements Executor for local execution (mode=local).
// Used when running directly on the target VM (e.g., via SSM).
type LocalExecutor struct {
	config  *Config
	verbose bool
}

// NewLocalExecutor creates a local executor.
func NewLocalExecutor(cfg *Config) *LocalExecutor {
	return &LocalExecutor{
		config:  cfg,
		verbose: cfg.Verbose,
	}
}

func (e *LocalExecutor) Exec(cmd string) (*ExecResult, error) {
	if e.verbose {
		fmt.Printf("[LOCAL] Executing: %s\n", cmd)
	}

	c := exec.Command("bash", "-c", cmd)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	exitCode := 0
	if err := c.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("exec failed: %w", err)
		}
	}

	return &ExecResult{
		Stdout:   strings.TrimSpace(stdout.String()),
		Stderr:   strings.TrimSpace(stderr.String()),
		ExitCode: exitCode,
	}, nil
}

func (e *LocalExecutor) ExecSudo(cmd string) (*ExecResult, error) {
	password := e.config.Password
	if e.config.SudoPassword != "" {
		password = e.config.SudoPassword
	}

	if password == "" {
		return e.Exec("sudo bash -c " + shellQuote(cmd))
	}

	sudoCmd := fmt.Sprintf("echo %s | sudo -S bash -c %s 2>/dev/null",
		shellQuote(password), shellQuote(cmd))
	return e.Exec(sudoCmd)
}

func (e *LocalExecutor) Upload(localPath string, remotePath string) error {
	if e.verbose {
		fmt.Printf("[LOCAL] Copying %s → %s\n", localPath, remotePath)
	}

	src, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(remotePath)
	if err != nil {
		return fmt.Errorf("create dest: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("copy failed: %w", err)
	}
	return nil
}

func (e *LocalExecutor) UploadBytes(data []byte, remotePath string, mode uint32) error {
	return e.UploadReader(bytes.NewReader(data), remotePath, int64(len(data)), mode)
}

func (e *LocalExecutor) UploadReader(reader io.Reader, remotePath string, size int64, mode uint32) error {
	if e.verbose {
		fmt.Printf("[LOCAL] Writing %d bytes → %s\n", size, remotePath)
	}

	f, err := os.Create(remotePath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, reader); err != nil {
		return fmt.Errorf("write failed: %w", err)
	}

	if mode != 0 {
		if err := os.Chmod(remotePath, os.FileMode(mode)); err != nil {
			return fmt.Errorf("chmod failed: %w", err)
		}
	}
	return nil
}

func (e *LocalExecutor) Close() error {
	return nil
}
