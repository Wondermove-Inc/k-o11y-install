package ssh

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// SSHExecutor implements Executor for direct SSH connections (mode=ssh).
type SSHExecutor struct {
	client  *ssh.Client
	config  *Config
	verbose bool
}

// NewSSHExecutor creates a new SSH connection to the target host.
func NewSSHExecutor(cfg *Config) (*SSHExecutor, error) {
	authMethods, err := buildAuthMethods(cfg)
	if err != nil {
		return nil, fmt.Errorf("auth setup failed: %w", err)
	}

	sshConfig := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	client, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		return nil, fmt.Errorf("SSH dial %s failed: %w", addr, err)
	}

	return &SSHExecutor{
		client:  client,
		config:  cfg,
		verbose: cfg.Verbose,
	}, nil
}

func (e *SSHExecutor) Exec(cmd string) (*ExecResult, error) {
	if e.verbose {
		fmt.Printf("[SSH] Executing on %s: %s\n", e.config.Host, cmd)
	}

	session, err := e.client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("session creation failed: %w", err)
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	exitCode := 0
	if err := session.Run(cmd); err != nil {
		if exitErr, ok := err.(*ssh.ExitError); ok {
			exitCode = exitErr.ExitStatus()
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

func (e *SSHExecutor) ExecSudo(cmd string) (*ExecResult, error) {
	password := e.config.Password
	if e.config.SudoPassword != "" {
		password = e.config.SudoPassword
	}

	if password == "" {
		// passwordless sudo
		return e.Exec("sudo bash -c " + shellQuote(cmd))
	}

	// sudo with password via stdin
	sudoCmd := fmt.Sprintf("echo %s | sudo -S bash -c %s 2>/dev/null",
		shellQuote(password), shellQuote(cmd))
	return e.Exec(sudoCmd)
}

func (e *SSHExecutor) Upload(localPath string, remotePath string) error {
	if e.verbose {
		fmt.Printf("[SSH] Uploading %s → %s:%s\n", localPath, e.config.Host, remotePath)
	}

	sftpClient, err := sftp.NewClient(e.client)
	if err != nil {
		return fmt.Errorf("SFTP client creation failed: %w", err)
	}
	defer sftpClient.Close()

	localFile, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open local file failed: %w", err)
	}
	defer localFile.Close()

	remoteFile, err := sftpClient.Create(remotePath)
	if err != nil {
		return fmt.Errorf("create remote file failed: %w", err)
	}
	defer remoteFile.Close()

	if _, err := io.Copy(remoteFile, localFile); err != nil {
		return fmt.Errorf("file copy failed: %w", err)
	}

	return nil
}

func (e *SSHExecutor) UploadBytes(data []byte, remotePath string, mode uint32) error {
	return e.UploadReader(bytes.NewReader(data), remotePath, int64(len(data)), mode)
}

func (e *SSHExecutor) UploadReader(reader io.Reader, remotePath string, size int64, mode uint32) error {
	if e.verbose {
		fmt.Printf("[SSH] Writing %d bytes → %s:%s\n", size, e.config.Host, remotePath)
	}

	sftpClient, err := sftp.NewClient(e.client)
	if err != nil {
		return fmt.Errorf("SFTP client creation failed: %w", err)
	}
	defer sftpClient.Close()

	remoteFile, err := sftpClient.Create(remotePath)
	if err != nil {
		return fmt.Errorf("create remote file failed: %w", err)
	}
	defer remoteFile.Close()

	if _, err := io.Copy(remoteFile, reader); err != nil {
		return fmt.Errorf("write failed: %w", err)
	}

	if mode != 0 {
		if err := sftpClient.Chmod(remotePath, os.FileMode(mode)); err != nil {
			return fmt.Errorf("chmod failed: %w", err)
		}
	}

	return nil
}

func (e *SSHExecutor) Close() error {
	return e.client.Close()
}

// buildAuthMethods constructs SSH auth methods from config.
func buildAuthMethods(cfg *Config) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	// Key-based auth
	if cfg.KeyPath != "" {
		key, err := os.ReadFile(cfg.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("read key file %s: %w", cfg.KeyPath, err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("parse key file: %w", err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}

	// Password auth
	if cfg.Password != "" {
		methods = append(methods, ssh.Password(cfg.Password))
	}

	if len(methods) == 0 {
		return nil, fmt.Errorf("no auth method configured (set --ssh-key or --ssh-password)")
	}

	return methods, nil
}

// shellQuote wraps a string in single quotes for shell safety.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
