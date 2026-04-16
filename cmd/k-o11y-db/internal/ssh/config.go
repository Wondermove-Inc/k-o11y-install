package ssh

// Config holds SSH connection parameters.
type Config struct {
	Mode     string // "ssh", "bastion", "local"
	Host     string // target host IP
	Port     int    // SSH port (default: 22)
	User     string // SSH user (default: "ubuntu")
	KeyPath  string // path to SSH private key
	Password string // SSH/sudo password

	// Bastion mode
	BastionHost string
	BastionPort int
	BastionUser string
	BastionKey  string

	// Sudo password (if different from SSH password)
	SudoPassword string

	// Verbose logging
	Verbose bool
}

// NewConfig creates a Config with defaults.
func NewConfig() *Config {
	return &Config{
		Mode: "ssh",
		Port: 22,
		User: "ubuntu",
	}
}
