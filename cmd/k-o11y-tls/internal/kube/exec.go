package kube

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-tls/internal/logger"
)

// KubeRunner wraps kubectl and helm execution via os/exec.
type KubeRunner struct {
	Context string // --kube-context value (uses the default context when empty)
	Verbose bool
	DryRun  bool
}

// Kubectl runs a kubectl command.
func (k *KubeRunner) Kubectl(args ...string) (string, error) {
	if k.Context != "" {
		args = append([]string{"--context", k.Context}, args...)
	}
	return k.run("kubectl", args...)
}

// KubectlApplyStdin applies YAML by passing it to kubectl via stdin.
func (k *KubeRunner) KubectlApplyStdin(yaml string) error {
	args := []string{"apply", "-f", "-"}
	if k.Context != "" {
		args = append([]string{"--context", k.Context}, args...)
	}

	if k.DryRun {
		logger.Info("[DRY-RUN] kubectl %s", strings.Join(args, " "))
		fmt.Println("---")
		fmt.Println(yaml)
		fmt.Println("---")
		return nil
	}

	if k.Verbose {
		logger.Info("kubectl %s", strings.Join(args, " "))
	}

	cmd := exec.Command("kubectl", args...)
	cmd.Stdin = strings.NewReader(yaml)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kubectl apply failed: %s\n%s", err, stderr.String())
	}

	if k.Verbose {
		out := strings.TrimSpace(stdout.String())
		if out != "" {
			fmt.Println(out)
		}
	}
	return nil
}

// Helm runs a helm command.
func (k *KubeRunner) Helm(args ...string) (string, error) {
	if k.Context != "" {
		args = append([]string{"--kube-context", k.Context}, args...)
	}
	return k.run("helm", args...)
}

// run executes a command and returns stdout.
func (k *KubeRunner) run(name string, args ...string) (string, error) {
	if k.DryRun {
		logger.Info("[DRY-RUN] %s %s", name, strings.Join(args, " "))
		return "", nil
	}

	if k.Verbose {
		logger.Info("%s %s", name, strings.Join(args, " "))
	}

	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s failed: %s\n%s", name, strings.Join(args[:min(len(args), 2)], " "), err, stderr.String())
	}

	return strings.TrimSpace(stdout.String()), nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
