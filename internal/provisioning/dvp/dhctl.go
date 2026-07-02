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
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/deckhouse/storage-e2e/internal/config"
)

const (
	dhctlContainerSSHKeyPath = "/root/.ssh/id_rsa"

	writeFileHeredocMarker = "STORAGE_E2E_B64_EOF"
)

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

	inner := fmt.Sprintf("sudo docker run --network=host --pull=always %s %s dhctl bootstrap %s > %s 2>&1",
		volFlags, p.InstallImage, sshArgs, p.RemoteLogPath)
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

	return p.runDhctlBootstrap(ctx, exec, def)
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

// persistBootstrapLog writes the dhctl log to a temp file and returns its path,
// or "" on failure (the content is still available for error wrapping).
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

// firstMasterVMIP returns the IP of the first VM master with an address filled in.
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
