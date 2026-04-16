package kube

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/Wondermove-Inc/k-o11y-install/cmd/k-o11y-tls/internal/logger"
)

// KubeRunner는 kubectl/helm 명령어를 os/exec로 실행하는 래퍼입니다.
type KubeRunner struct {
	Context string // --kube-context 값 (비어있으면 기본 컨텍스트 사용)
	Verbose bool
	DryRun  bool
}

// Kubectl은 kubectl 명령어를 실행합니다.
func (k *KubeRunner) Kubectl(args ...string) (string, error) {
	if k.Context != "" {
		args = append([]string{"--context", k.Context}, args...)
	}
	return k.run("kubectl", args...)
}

// KubectlApplyStdin은 YAML을 stdin으로 전달하여 kubectl apply합니다.
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

// Helm은 helm 명령어를 실행합니다.
func (k *KubeRunner) Helm(args ...string) (string, error) {
	if k.Context != "" {
		args = append([]string{"--kube-context", k.Context}, args...)
	}
	return k.run("helm", args...)
}

// run은 명령어를 실행하고 stdout을 반환합니다.
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
