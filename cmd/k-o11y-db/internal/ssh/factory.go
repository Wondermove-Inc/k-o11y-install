package ssh

import "fmt"

// NewExecutor creates an Executor based on the connection mode.
func NewExecutor(cfg *Config) (Executor, error) {
	switch cfg.Mode {
	case "ssh":
		return NewSSHExecutor(cfg)
	case "bastion":
		if cfg.BastionHost == "" {
			return nil, fmt.Errorf("--bastion-host is required for bastion mode")
		}
		return NewBastionExecutor(cfg)
	case "local":
		return NewLocalExecutor(cfg), nil
	default:
		return nil, fmt.Errorf("unknown mode: %s (supported: ssh, bastion, local)", cfg.Mode)
	}
}
