// Package ssh provides SSH connection abstraction for remote command execution.
// Supports 3 modes: ssh (direct), bastion (jump host), local (exec.Command).
package ssh

import "io"

// ExecResult holds the result of a remote command execution.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Executor abstracts remote command execution across 3 connection modes.
type Executor interface {
	// Exec runs a command on the remote host.
	Exec(cmd string) (*ExecResult, error)

	// ExecSudo runs a command with sudo on the remote host.
	ExecSudo(cmd string) (*ExecResult, error)

	// Upload copies a local file to the remote host.
	Upload(localPath string, remotePath string) error

	// UploadBytes writes byte content to a file on the remote host.
	UploadBytes(data []byte, remotePath string, mode uint32) error

	// UploadReader writes from a reader to a file on the remote host.
	UploadReader(reader io.Reader, remotePath string, size int64, mode uint32) error

	// Close closes the connection.
	Close() error
}
