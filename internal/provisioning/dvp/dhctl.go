/*
 * Copyright 2026 Flant JSC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * 	http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package dvp

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/config"
)

const (
	dhctlContainerSSHKeyPath = "/root/.ssh/id_rsa"

	writeFileHeredocMarker = "STORAGE_E2E_B64_EOF"

	dhctlContainerName = "storage-e2e-dhctl-bootstrap"
)

const masterKubeconfigProbeCmd = "sudo -n test -f /etc/kubernetes/super-admin.conf" +
	" || sudo -n test -f /etc/kubernetes/admin.conf"

type dhctlBootstrapParams struct {
	InstallImage  string // e.g. registryRepo + "/install:" + devBranch
	VMSSHUser     string // login on the setup node / masters
	MasterIP      string // first master dhctl bootstraps
	RemoteLogPath string // where the container's combined output is redirected
	UsePassphrase bool   // true → --connection-config; false → --ssh-agent-private-keys
}

func remoteConfigPath(user string) string { return "/home/" + user + "/config.yml" }

func remoteKeyPath(user string) string { return "/home/" + user + "/.ssh/id_rsa" }

func remoteConnConfigPath(user string) string {
	return "/home/" + user + "/.config/storage-e2e/dhctl-connection.yaml"
}

func buildDockerLoginCommand(registryHost, licenseKey string) string {
	return fmt.Sprintf("echo %q | sudo docker login -u license-token --password-stdin %s", licenseKey, registryHost)
}

func buildWriteFileCommand(remotePath string, content []byte, mode string) string {
	b64 := base64.StdEncoding.EncodeToString(content)
	return fmt.Sprintf(
		"set -eu\numask 077\nmkdir -p %q\nsudo rm -rf -- %q\nbase64 -d > %q <<'%s'\n%s\n%s\nchmod %s %q",
		path.Dir(remotePath), remotePath, remotePath, writeFileHeredocMarker, b64, writeFileHeredocMarker, mode, remotePath,
	)
}

func buildBindMount(src, dst string, readonly bool) string {
	m := fmt.Sprintf("type=bind,src=%s,dst=%s", src, dst)
	if readonly {
		m += ",readonly"
	}
	return fmt.Sprintf("--mount %q", m)
}

func buildDhctlBootstrapCommand(p dhctlBootstrapParams) string {
	configMount := buildBindMount(remoteConfigPath(p.VMSSHUser), "/config.yml", false)

	var volFlags, sshArgs string
	if p.UsePassphrase {
		connMount := buildBindMount(remoteConnConfigPath(p.VMSSHUser), "/dhctl-connection.yaml", true)
		volFlags = configMount + " " + connMount
		sshArgs = "--connection-config=/dhctl-connection.yaml --config=/config.yml"
	} else {
		keyMount := buildBindMount(remoteKeyPath(p.VMSSHUser), dhctlContainerSSHKeyPath, true)
		volFlags = configMount + " " + keyMount
		sshArgs = fmt.Sprintf("--ssh-host=%s --ssh-user=%s --ssh-agent-private-keys=%s --config=/config.yml",
			p.MasterIP, p.VMSSHUser, dhctlContainerSSHKeyPath)
	}

	inner := fmt.Sprintf("sudo docker run --name %s --network=host --pull=always %s %s dhctl bootstrap %s > %s 2>&1",
		dhctlContainerName, volFlags, p.InstallImage, sshArgs, p.RemoteLogPath)
	return fmt.Sprintf("sudo -u %s bash -c %s", p.VMSSHUser, shellSingleQuote(inner))
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

type dhctlSSHConfigManifest struct {
	APIVersion          string                    `yaml:"apiVersion"`
	Kind                string                    `yaml:"kind"`
	SSHUser             string                    `yaml:"sshUser"`
	SSHPort             int32                     `yaml:"sshPort"`
	SSHAgentPrivateKeys []dhctlSSHAgentPrivateKey `yaml:"sshAgentPrivateKeys"`
}

type dhctlSSHAgentPrivateKey struct {
	Key        string `yaml:"key"`
	Passphrase string `yaml:"passphrase,omitempty"`
}

type dhctlSSHHostManifest struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Host       string `yaml:"host"`
}

func buildDHCTLSSHConnectionConfig(privateKeyPEM, user, masterIP, passphrase string) ([]byte, error) {
	if strings.TrimSpace(privateKeyPEM) == "" {
		return nil, fmt.Errorf("private key PEM is empty")
	}
	if user == "" {
		return nil, fmt.Errorf("ssh user is empty")
	}
	if masterIP == "" {
		return nil, fmt.Errorf("master IP is empty")
	}

	cfg := dhctlSSHConfigManifest{
		APIVersion: "dhctl.deckhouse.io/v1",
		Kind:       "SSHConfig",
		SSHUser:    user,
		SSHPort:    22,
		SSHAgentPrivateKeys: []dhctlSSHAgentPrivateKey{{
			Key:        strings.TrimSpace(privateKeyPEM) + "\n",
			Passphrase: passphrase,
		}},
	}
	hostDoc := dhctlSSHHostManifest{
		APIVersion: "dhctl.deckhouse.io/v1",
		Kind:       "SSHHost",
		Host:       masterIP,
	}

	cfgBytes, err := yaml.Marshal(&cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal SSHConfig: %w", err)
	}
	hostBytes, err := yaml.Marshal(&hostDoc)
	if err != nil {
		return nil, fmt.Errorf("marshal SSHHost: %w", err)
	}
	doc := "---\n" + strings.TrimSuffix(string(cfgBytes), "\n") + "\n---\n" + strings.TrimSuffix(string(hostBytes), "\n") + "\n"
	return []byte(doc), nil
}

func (p *dvpProvider) dhctlBootstrap(ctx context.Context, def *config.ClusterDefinition) error {
	if def.Setup == nil || def.Setup.IPAddress == "" {
		return fmt.Errorf("dhctl bootstrap: setup node IP is not set")
	}

	exec, closeExec, err := p.deps.connector.VMExecutor(ctx, def.Setup.IPAddress)
	if err != nil {
		return fmt.Errorf("dhctl bootstrap: connect to setup node: %w", err)
	}
	defer closeExec()

	return p.ensureDhctlBootstrapped(ctx, exec, def)
}

func (p *dvpProvider) ensureDhctlBootstrapped(ctx context.Context, exec remoteExecutor, def *config.ClusterDefinition) error {
	masterIP, err := firstMasterVMIP(def)
	if err != nil {
		return fmt.Errorf("dhctl bootstrap: %w", err)
	}

	status, err := dockerContainerStatus(ctx, exec, dhctlContainerName)
	if err != nil {
		return fmt.Errorf("dhctl bootstrap: check for in-flight bootstrap container: %w", err)
	}
	if status == "running" {
		p.logger.Info("found in-flight dhctl bootstrap container from a previous run, waiting for it to finish",
			"container", dhctlContainerName)
		exitCode, waitErr := dockerWaitContainer(ctx, exec, dhctlContainerName)
		if waitErr != nil {
			return fmt.Errorf("dhctl bootstrap: wait for in-flight bootstrap container: %w", waitErr)
		}
		p.logger.Info("in-flight dhctl bootstrap container finished",
			"container", dhctlContainerName, "exitCode", exitCode)
	}

	installed, err := p.deckhouseAlreadyInstalled(ctx, masterIP)
	if err != nil {
		return err
	}
	if installed {
		p.logger.Info("Deckhouse is already installed on the first master, skipping dhctl bootstrap",
			"masterIP", masterIP)
		return nil
	}

	return p.runDhctlBootstrap(ctx, exec, def)
}

func (p *dvpProvider) deckhouseAlreadyInstalled(ctx context.Context, masterIP string) (bool, error) {
	exec, closeExec, err := p.deps.connector.VMExecutor(ctx, masterIP)
	if err != nil {
		return false, fmt.Errorf("dhctl bootstrap: probe master %s: connect: %w", masterIP, err)
	}
	res, probeErr := exec.Exec(ctx, masterKubeconfigProbeCmd)
	closeExec()
	if probeErr != nil {
		if res.ExitCode != 0 {
			return false, nil // no kubeconfig — clean master
		}
		return false, fmt.Errorf("dhctl bootstrap: probe master %s for kubeconfig: %w", masterIP, probeErr)
	}

	p.logger.Info("master already has a kubeconfig, checking existing installation health", "masterIP", masterIP)
	target, cleanup, err := p.deps.masterConn.connectToMaster(ctx, masterIP)
	if err != nil {
		return false, fmt.Errorf("master %s has a kubeconfig but its API server is unreachable; "+
			"the VM is half-bootstrapped and dhctl cannot resume it — recreate the cluster VMs: %w", masterIP, err)
	}
	defer cleanup()

	if err := p.installReadyWait()(ctx, target, existingInstallReadyTimeout); err != nil {
		return false, fmt.Errorf("master %s has a kubeconfig but Deckhouse never became healthy; "+
			"the VM is half-bootstrapped — recreate the cluster VMs or inspect `sudo docker logs %s` on the setup node: %w",
			masterIP, dhctlContainerName, err)
	}
	return true, nil
}

func (p *dvpProvider) installReadyWait() func(context.Context, *rest.Config, time.Duration) error {
	if p.deps.installReady != nil {
		return p.deps.installReady
	}
	return waitExistingInstallReady
}

// dockerContainerStatus returns the container's State.Status ("running",
// "exited", …) or "" when no such container exists.
func dockerContainerStatus(ctx context.Context, exec remoteExecutor, name string) (string, error) {
	cmd := fmt.Sprintf("sudo docker inspect -f '{{.State.Status}}' %q 2>/dev/null || true", name)
	res, err := exec.Exec(ctx, cmd)
	if err != nil {
		return "", fmt.Errorf("inspect container %q: %w", name, err)
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

// dockerWaitContainer blocks until the container stops and returns its exit code.
func dockerWaitContainer(ctx context.Context, exec remoteExecutor, name string) (int, error) {
	res, err := exec.Exec(ctx, fmt.Sprintf("sudo docker wait %q", name))
	if err != nil {
		return 0, fmt.Errorf("docker wait %q: %w", name, err)
	}
	out := strings.TrimSpace(string(res.Stdout))
	code, convErr := strconv.Atoi(out)
	if convErr != nil {
		return 0, fmt.Errorf("docker wait %q: unexpected output %q", name, out)
	}
	return code, nil
}

func (p *dvpProvider) runDhctlBootstrap(ctx context.Context, exec remoteExecutor, def *config.ClusterDefinition) error {
	masterIP, err := firstMasterVMIP(def)
	if err != nil {
		return fmt.Errorf("dhctl bootstrap: %w", err)
	}

	registryRepo := def.DKPParameters.RegistryRepo
	if registryRepo == "" {
		return fmt.Errorf("dhctl bootstrap: dkpParameters.registryRepo is required")
	}
	registryHost, _, _ := strings.Cut(registryRepo, "/")

	devBranch := def.DKPParameters.DevBranch
	if devBranch == "" {
		devBranch = "main"
	}
	installImage := fmt.Sprintf("%s/install:%s", registryRepo, devBranch)

	params, err := buildBootstrapParams(def, p.dvpConf.RegistryDockerCfg)
	if err != nil {
		return fmt.Errorf("dhctl bootstrap: build config params: %w", err)
	}
	configYML, err := renderBootstrapConfig(params)
	if err != nil {
		return fmt.Errorf("dhctl bootstrap: render config: %w", err)
	}

	user := p.dvpConf.VMSSHUser
	usePassphrase := p.dvpConf.SSHPassphrase != ""

	if res, err := exec.Exec(ctx, buildWriteFileCommand(remoteConfigPath(user), configYML, "0644")); err != nil {
		return fmt.Errorf("dhctl bootstrap: write config.yml to setup node: %w (stderr: %s)", err, string(res.Stderr))
	}
	if res, err := exec.Exec(ctx, buildWriteFileCommand(remoteKeyPath(user), p.creds.SSHKey, "0600")); err != nil {
		return fmt.Errorf("dhctl bootstrap: write private key to setup node: %w (stderr: %s)", err, string(res.Stderr))
	}

	if res, err := exec.Exec(ctx, buildDockerLoginCommand(registryHost, p.dvpConf.DKPLicenseKey)); err != nil {
		return fmt.Errorf("dhctl bootstrap: registry login to %s: %w (stderr: %s)", registryHost, err, string(res.Stderr))
	}

	if usePassphrase {
		connYAML, err := buildDHCTLSSHConnectionConfig(string(p.creds.SSHKey), user, masterIP, p.dvpConf.SSHPassphrase)
		if err != nil {
			return fmt.Errorf("dhctl bootstrap: build connection-config: %w", err)
		}
		if res, err := exec.Exec(ctx, buildWriteFileCommand(remoteConnConfigPath(user), connYAML, "0600")); err != nil {
			return fmt.Errorf("dhctl bootstrap: write connection-config to setup node: %w (stderr: %s)", err, string(res.Stderr))
		}
	}

	if _, err := exec.Exec(ctx, fmt.Sprintf("sudo docker rm -f %q >/dev/null 2>&1 || true", dhctlContainerName)); err != nil {
		p.logger.Warn("failed to remove leftover dhctl bootstrap container", "container", dhctlContainerName, "err", err)
	}

	remoteLog := fmt.Sprintf("/tmp/dhctl-bootstrap-%d.log", time.Now().UnixNano())
	bootstrapCmd := buildDhctlBootstrapCommand(dhctlBootstrapParams{
		InstallImage:  installImage,
		VMSSHUser:     user,
		MasterIP:      masterIP,
		RemoteLogPath: remoteLog,
		UsePassphrase: usePassphrase,
	})

	_, bootstrapErr := exec.Exec(ctx, bootstrapCmd)

	cleanupCtx := context.WithoutCancel(ctx)
	if usePassphrase {
		if _, err := exec.Exec(cleanupCtx, fmt.Sprintf("sudo rm -rf -- %q", remoteConnConfigPath(user))); err != nil {
			p.logger.Warn("failed to remove dhctl connection-config from setup node", "err", err)
		}
	}

	logContent := p.collectRemoteLog(cleanupCtx, exec, remoteLog)

	if bootstrapErr != nil {
		if len(logContent) > 0 {
			return fmt.Errorf("dhctl bootstrap failed: %w\n\nbootstrap log:\n%s", bootstrapErr, logContent)
		}
		return fmt.Errorf("dhctl bootstrap failed: %w", bootstrapErr)
	}
	return nil
}

func (p *dvpProvider) collectRemoteLog(ctx context.Context, exec remoteExecutor, remoteLog string) []byte {
	res, err := exec.Exec(ctx, fmt.Sprintf("sudo cat %q 2>/dev/null || true", remoteLog))
	logContent := res.Stdout
	if err != nil {
		p.logger.Warn("failed to read dhctl bootstrap log from setup node", "err", err)
	}

	if _, rmErr := exec.Exec(ctx, fmt.Sprintf("sudo rm -f %q", remoteLog)); rmErr != nil {
		p.logger.Warn("failed to remove dhctl bootstrap log from setup node", "err", rmErr)
	}

	if len(logContent) > 0 {
		if localPath := persistBootstrapLog(logContent); localPath != "" {
			p.logger.Info("saved dhctl bootstrap log", "path", localPath)
		}
	}
	return logContent
}

func persistBootstrapLog(content []byte) string {
	f, err := os.CreateTemp("", "dhctl-bootstrap-*.log")
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := f.Write(content); err != nil {
		return ""
	}
	return f.Name()
}

func firstMasterVMIP(def *config.ClusterDefinition) (string, error) {
	if def == nil {
		return "", fmt.Errorf("cluster definition is nil")
	}
	for _, m := range def.Masters {
		if m.HostType == config.HostTypeVM && m.IPAddress != "" {
			return m.IPAddress, nil
		}
	}
	return "", fmt.Errorf("no master VM with an IP address in cluster definition")
}
