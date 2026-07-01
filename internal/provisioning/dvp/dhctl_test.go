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
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/deckhouse/storage-e2e/internal/config"
	sshv2 "github.com/deckhouse/storage-e2e/internal/infrastructure/ssh/v2"
	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
)

type funcExecutor struct {
	mu   sync.Mutex
	cmds []string
	fn   func(ctx context.Context, cmd string) (sshv2.ExecResult, error)
}

func (e *funcExecutor) Exec(ctx context.Context, cmd string) (sshv2.ExecResult, error) {
	e.mu.Lock()
	e.cmds = append(e.cmds, cmd)
	fn := e.fn
	e.mu.Unlock()
	if fn != nil {
		return fn(ctx, cmd)
	}
	return sshv2.ExecResult{}, nil
}

func (e *funcExecutor) recorded() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.cmds...)
}

// indexMatching returns the index of the first recorded command satisfying pred,
// or -1.
func indexMatching(cmds []string, pred func(string) bool) int {
	for i, c := range cmds {
		if pred(c) {
			return i
		}
	}
	return -1
}

func contains(sub ...string) func(string) bool {
	return func(c string) bool {
		for _, s := range sub {
			if !strings.Contains(c, s) {
				return false
			}
		}
		return true
	}
}

func TestBuildDockerLoginCommand(t *testing.T) {
	t.Parallel()
	const want = `echo "LICENSE-123" | sudo docker login -u license-token --password-stdin dev-registry.deckhouse.io`
	if got := buildDockerLoginCommand("dev-registry.deckhouse.io", "LICENSE-123"); got != want {
		t.Errorf("buildDockerLoginCommand()\n got = %q\nwant = %q", got, want)
	}
}

func TestBuildDhctlBootstrapCommandKeyFile(t *testing.T) {
	t.Parallel()
	got := buildDhctlBootstrapCommand(dhctlBootstrapParams{
		InstallImage:  "dev-registry.deckhouse.io/sys/deckhouse-oss/install:main",
		VMSSHUser:     "cloud",
		MasterIP:      "10.10.1.5",
		RemoteLogPath: "/tmp/dhctl-bootstrap-1.log",
		UsePassphrase: false,
	})
	const want = `sudo -u cloud bash -c 'sudo docker run --network=host --pull=always ` +
		`-v "/home/cloud/config.yml:/config.yml" -v "/home/cloud/.ssh/id_rsa:/root/.ssh/id_rsa:ro" ` +
		`dev-registry.deckhouse.io/sys/deckhouse-oss/install:main dhctl bootstrap ` +
		`--ssh-host=10.10.1.5 --ssh-user=cloud --ssh-agent-private-keys=/root/.ssh/id_rsa --config=/config.yml ` +
		`> /tmp/dhctl-bootstrap-1.log 2>&1'`
	if got != want {
		t.Errorf("buildDhctlBootstrapCommand(key-file)\n got = %q\nwant = %q", got, want)
	}
}

func TestBuildDhctlBootstrapCommandPassphrase(t *testing.T) {
	t.Parallel()
	got := buildDhctlBootstrapCommand(dhctlBootstrapParams{
		InstallImage:  "dev-registry.deckhouse.io/sys/deckhouse-oss/install:main",
		VMSSHUser:     "cloud",
		MasterIP:      "10.10.1.5",
		RemoteLogPath: "/tmp/dhctl-bootstrap-1.log",
		UsePassphrase: true,
	})
	const want = `sudo -u cloud bash -c 'sudo docker run --network=host --pull=always ` +
		`-v "/home/cloud/config.yml:/config.yml" -v "/home/cloud/.config/storage-e2e/dhctl-connection.yaml:/dhctl-connection.yaml:ro" ` +
		`dev-registry.deckhouse.io/sys/deckhouse-oss/install:main dhctl bootstrap ` +
		`--connection-config=/dhctl-connection.yaml --config=/config.yml ` +
		`> /tmp/dhctl-bootstrap-1.log 2>&1'`
	if got != want {
		t.Errorf("buildDhctlBootstrapCommand(passphrase)\n got = %q\nwant = %q", got, want)
	}
}

func TestBuildDHCTLSSHConnectionConfig(t *testing.T) {
	t.Parallel()

	out, err := buildDHCTLSSHConnectionConfig("PRIVATE-KEY-PEM", "cloud", "10.10.1.5", "s3cret")
	if err != nil {
		t.Fatalf("buildDHCTLSSHConnectionConfig() error = %v", err)
	}
	s := string(out)
	for _, want := range []string{
		"kind: SSHConfig",
		"sshUser: cloud",
		"sshPort: 22",
		"PRIVATE-KEY-PEM",
		"passphrase: s3cret",
		"kind: SSHHost",
		"host: 10.10.1.5",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("connection-config missing %q\n---\n%s", want, s)
		}
	}
	if !strings.HasPrefix(s, "---\n") {
		t.Errorf("connection-config should start with document separator\n%s", s)
	}
}

func TestBuildDHCTLSSHConnectionConfigErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                        string
		pem, user, masterIP, phrase string
	}{
		{"empty key", "  ", "cloud", "10.10.1.5", ""},
		{"empty user", "PEM", "", "10.10.1.5", ""},
		{"empty master", "PEM", "cloud", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := buildDHCTLSSHConnectionConfig(tt.pem, tt.user, tt.masterIP, tt.phrase); err == nil {
				t.Errorf("buildDHCTLSSHConnectionConfig(%q) error = nil, want error", tt.name)
			}
		})
	}
}

func TestBuildWriteFileCommand(t *testing.T) {
	t.Parallel()
	got := buildWriteFileCommand("/home/cloud/config.yml", []byte("hello"), "0644")
	for _, want := range []string{
		`mkdir -p "/home/cloud"`,
		`base64 -d > "/home/cloud/config.yml" <<'STORAGE_E2E_B64_EOF'`,
		"aGVsbG8=", // base64("hello")
		`chmod 0644 "/home/cloud/config.yml"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("buildWriteFileCommand missing %q\n---\n%s", want, got)
		}
	}
}

func dhctlTestProvider(t *testing.T, dvpConf *Config) *dvpProvider {
	t.Helper()
	creds := Credentials{SSHKey: testPrivateKeyPEM(t)}
	return newProvider(quietLogger(), &clusterprovider.ClusterConfig{}, dvpConf, creds, deps{})
}

func dhctlTestDef() *config.ClusterDefinition {
	return withRequiredDKP(&config.ClusterDefinition{
		Masters: []config.ClusterNode{
			{Hostname: "m1", HostType: config.HostTypeVM, IPAddress: "10.10.1.5"},
		},
	})
}

func TestRunDhctlBootstrapKeyFileCleanupOnFailure(t *testing.T) {
	t.Parallel()

	exec := &funcExecutor{fn: func(_ context.Context, cmd string) (sshv2.ExecResult, error) {
		switch {
		case strings.Contains(cmd, "dhctl bootstrap"):
			return sshv2.ExecResult{Stderr: []byte("boom")}, errors.New("exit status 1")
		case strings.Contains(cmd, "cat") && strings.Contains(cmd, "dhctl-bootstrap-"):
			return sshv2.ExecResult{Stdout: []byte("LOG-CONTENT")}, nil
		default:
			return sshv2.ExecResult{}, nil
		}
	}}

	p := dhctlTestProvider(t, &Config{VMSSHUser: "cloud", DKPLicenseKey: "LIC", RegistryDockerCfg: "CFG"})

	err := p.runDhctlBootstrap(context.Background(), exec, dhctlTestDef())
	if err == nil {
		t.Fatal("runDhctlBootstrap() error = nil, want bootstrap failure")
	}
	if !strings.Contains(err.Error(), "LOG-CONTENT") {
		t.Errorf("error should wrap remote log content, got: %v", err)
	}

	cmds := exec.recorded()
	iConfig := indexMatching(cmds, contains("/home/cloud/config.yml", "base64 -d"))
	iKey := indexMatching(cmds, contains("/home/cloud/.ssh/id_rsa", "base64 -d"))
	iLogin := indexMatching(cmds, contains("docker login"))
	iRun := indexMatching(cmds, contains("dhctl bootstrap"))
	iCat := indexMatching(cmds, contains("cat", "dhctl-bootstrap-"))
	iRm := indexMatching(cmds, contains("rm -f", "dhctl-bootstrap-"))

	for name, idx := range map[string]int{"config": iConfig, "key": iKey, "login": iLogin, "run": iRun, "cat": iCat, "rm": iRm} {
		if idx < 0 {
			t.Fatalf("missing %s command in %v", name, cmds)
		}
	}
	if !(iConfig < iKey && iKey < iLogin && iLogin < iRun && iRun < iCat && iCat < iRm) {
		t.Errorf("unexpected command order: config=%d key=%d login=%d run=%d cat=%d rm=%d\n%v",
			iConfig, iKey, iLogin, iRun, iCat, iRm, cmds)
	}
	if indexMatching(cmds, contains("dhctl-connection.yaml")) != -1 {
		t.Errorf("key-file mode should not write a connection-config: %v", cmds)
	}
}

func TestRunDhctlBootstrapPassphraseCleanupOrder(t *testing.T) {
	t.Parallel()

	exec := &funcExecutor{fn: func(_ context.Context, cmd string) (sshv2.ExecResult, error) {
		if strings.Contains(cmd, "cat") && strings.Contains(cmd, "dhctl-bootstrap-") {
			return sshv2.ExecResult{Stdout: []byte("ok")}, nil
		}
		return sshv2.ExecResult{}, nil
	}}

	p := dhctlTestProvider(t, &Config{VMSSHUser: "cloud", DKPLicenseKey: "LIC", RegistryDockerCfg: "CFG", SSHPassphrase: "s3cret"})

	if err := p.runDhctlBootstrap(context.Background(), exec, dhctlTestDef()); err != nil {
		t.Fatalf("runDhctlBootstrap() error = %v, want success", err)
	}

	cmds := exec.recorded()
	iConnWrite := indexMatching(cmds, contains("dhctl-connection.yaml", "base64 -d"))
	iRun := indexMatching(cmds, contains("dhctl bootstrap"))
	iConnRm := indexMatching(cmds, contains("rm -f", "dhctl-connection.yaml"))
	iLogRm := indexMatching(cmds, contains("rm -f", "dhctl-bootstrap-"))

	if iConnWrite < 0 || iRun < 0 || iConnRm < 0 || iLogRm < 0 {
		t.Fatalf("missing expected command; connWrite=%d run=%d connRm=%d logRm=%d\n%v", iConnWrite, iRun, iConnRm, iLogRm, cmds)
	}
	if !(iConnWrite < iRun && iRun < iConnRm) {
		t.Errorf("connection-config must be written before and removed after the docker run: connWrite=%d run=%d connRm=%d\n%v",
			iConnWrite, iRun, iConnRm, cmds)
	}
}

func TestRunDhctlBootstrapRequiresLicense(t *testing.T) {
	t.Parallel()
	exec := &funcExecutor{}
	p := dhctlTestProvider(t, &Config{VMSSHUser: "cloud"})
	if err := p.runDhctlBootstrap(context.Background(), exec, dhctlTestDef()); err == nil {
		t.Fatal("runDhctlBootstrap() error = nil, want missing-license error")
	}
	if len(exec.recorded()) != 0 {
		t.Errorf("no commands should run without a license, got %v", exec.recorded())
	}
}
