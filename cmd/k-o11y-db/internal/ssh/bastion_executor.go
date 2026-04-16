package ssh

import (
	"fmt"
	"os"

	gossh "golang.org/x/crypto/ssh"
)

// BastionExecutor implements Executor for bastion/jump host connections (mode=bastion).
type BastionExecutor struct {
	SSHExecutor            // embed SSHExecutor for Exec/Upload reuse
	bastionClient *gossh.Client
}

// NewBastionExecutor creates a connection through a bastion host.
func NewBastionExecutor(cfg *Config) (*BastionExecutor, error) {
	// 1. Connect to bastion
	bastionAuth, err := buildBastionAuth(cfg)
	if err != nil {
		return nil, fmt.Errorf("bastion auth setup failed: %w", err)
	}

	bastionConfig := &gossh.ClientConfig{
		User:            cfg.BastionUser,
		Auth:            bastionAuth,
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
	}

	bastionAddr := fmt.Sprintf("%s:%d", cfg.BastionHost, cfg.BastionPort)
	bastionClient, err := gossh.Dial("tcp", bastionAddr, bastionConfig)
	if err != nil {
		return nil, fmt.Errorf("bastion dial %s failed: %w", bastionAddr, err)
	}

	// 2. Dial target through bastion
	targetAuth, err := buildAuthMethods(cfg)
	if err != nil {
		bastionClient.Close()
		return nil, fmt.Errorf("target auth setup failed: %w", err)
	}

	targetConfig := &gossh.ClientConfig{
		User:            cfg.User,
		Auth:            targetAuth,
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
	}

	targetAddr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	conn, err := bastionClient.Dial("tcp", targetAddr)
	if err != nil {
		bastionClient.Close()
		return nil, fmt.Errorf("dial target %s through bastion failed: %w", targetAddr, err)
	}

	ncc, chans, reqs, err := gossh.NewClientConn(conn, targetAddr, targetConfig)
	if err != nil {
		conn.Close()
		bastionClient.Close()
		return nil, fmt.Errorf("target SSH handshake failed: %w", err)
	}

	targetClient := gossh.NewClient(ncc, chans, reqs)

	return &BastionExecutor{
		SSHExecutor: SSHExecutor{
			client:  targetClient,
			config:  cfg,
			verbose: cfg.Verbose,
		},
		bastionClient: bastionClient,
	}, nil
}

func (e *BastionExecutor) Close() error {
	e.SSHExecutor.Close()
	return e.bastionClient.Close()
}

func buildBastionAuth(cfg *Config) ([]gossh.AuthMethod, error) {
	var methods []gossh.AuthMethod

	if cfg.BastionKey != "" {
		key, err := os.ReadFile(cfg.BastionKey)
		if err != nil {
			return nil, fmt.Errorf("read bastion key %s: %w", cfg.BastionKey, err)
		}
		signer, err := gossh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("parse bastion key: %w", err)
		}
		methods = append(methods, gossh.PublicKeys(signer))
	}

	if len(methods) == 0 {
		return nil, fmt.Errorf("bastion auth not configured (set --bastion-key)")
	}

	return methods, nil
}
