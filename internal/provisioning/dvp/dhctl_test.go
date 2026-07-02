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
	"time"

	"k8s.io/client-go/rest"

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
	const want = `sudo -u cloud bash -c 'sudo docker run --name storage-e2e-dhctl-bootstrap --network=host --pull=always ` +
		`--mount "type=bind,src=/home/cloud/config.yml,dst=/config.yml" ` +
		`--mount "type=bind,src=/home/cloud/.ssh/id_rsa,dst=/root/.ssh/id_rsa,readonly" ` +
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
	const want = `sudo -u cloud bash -c 'sudo docker run --name storage-e2e-dhctl-bootstrap --network=host --pull=always ` +
		`--mount "type=bind,src=/home/cloud/config.yml,dst=/config.yml" ` +
		`--mount "type=bind,src=/home/cloud/.config/storage-e2e/dhctl-connection.yaml,dst=/dhctl-connection.yaml,readonly" ` +
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
		`sudo rm -rf -- "/home/cloud/config.yml"`,
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
	// The write script also contains an `rm -rf` of the same path, so the
	// standalone cleanup command is identified by its "sudo rm -rf" prefix.
	iConnRm := indexMatching(cmds, func(c string) bool {
		return strings.HasPrefix(c, "sudo rm -rf") && strings.Contains(c, "dhctl-connection.yaml")
	})
	iLogRm := indexMatching(cmds, contains("rm -f", "dhctl-bootstrap-"))

	if iConnWrite < 0 || iRun < 0 || iConnRm < 0 || iLogRm < 0 {
		t.Fatalf("missing expected command; connWrite=%d run=%d connRm=%d logRm=%d\n%v", iConnWrite, iRun, iConnRm, iLogRm, cmds)
	}
	if !(iConnWrite < iRun && iRun < iConnRm) {
		t.Errorf("connection-config must be written before and removed after the docker run: connWrite=%d run=%d connRm=%d\n%v",
			iConnWrite, iRun, iConnRm, cmds)
	}
}

// --- ensureDhctlBootstrapped flow ---

// routeConnector hands out a per-IP funcExecutor (master probe path).
type routeConnector struct {
	execs map[string]*funcExecutor
}

func (c routeConnector) Connect(context.Context) (*rest.Config, func(), error) {
	return nil, nil, errors.New("Connect is not used in this test")
}

func (c routeConnector) VMExecutor(_ context.Context, ip string) (remoteExecutor, func(), error) {
	e, ok := c.execs[ip]
	if !ok {
		return nil, nil, errors.New("unexpected VMExecutor for " + ip)
	}
	return e, func() {}, nil
}

type fakeMasterConn struct{ err error }

func (f fakeMasterConn) connectToMaster(context.Context, string) (*rest.Config, func(), error) {
	if f.err != nil {
		return nil, nil, f.err
	}
	return &rest.Config{}, func() {}, nil
}

func ensureTestProvider(t *testing.T, d deps) *dvpProvider {
	t.Helper()
	dvpConf := &Config{VMSSHUser: "cloud", DKPLicenseKey: "LIC", RegistryDockerCfg: "CFG"}
	creds := Credentials{SSHKey: testPrivateKeyPEM(t)}
	return newProvider(quietLogger(), &clusterprovider.ClusterConfig{}, dvpConf, creds, d)
}

const testMasterIP = "10.10.1.5" // matches dhctlTestDef

func TestEnsureDhctlBootstrappedSkipsWhenInstalled(t *testing.T) {
	t.Parallel()

	setupExec := &funcExecutor{}
	masterExec := &funcExecutor{} // kubeconfig probe succeeds
	p := ensureTestProvider(t, deps{
		connector:    routeConnector{execs: map[string]*funcExecutor{testMasterIP: masterExec}},
		masterConn:   fakeMasterConn{},
		installReady: func(context.Context, *rest.Config, time.Duration) error { return nil },
	})

	if err := p.ensureDhctlBootstrapped(context.Background(), setupExec, dhctlTestDef()); err != nil {
		t.Fatalf("ensureDhctlBootstrapped() error = %v, want nil (skip path)", err)
	}
	if indexMatching(masterExec.recorded(), contains("test -f /etc/kubernetes")) == -1 {
		t.Errorf("master kubeconfig probe was not executed: %v", masterExec.recorded())
	}
	if indexMatching(setupExec.recorded(), contains("docker run")) != -1 {
		t.Errorf("dhctl must not run when Deckhouse is already installed: %v", setupExec.recorded())
	}
}

func TestEnsureDhctlBootstrappedRunsWhenMasterClean(t *testing.T) {
	t.Parallel()

	setupExec := &funcExecutor{fn: func(_ context.Context, cmd string) (sshv2.ExecResult, error) {
		if strings.Contains(cmd, "cat") && strings.Contains(cmd, "dhctl-bootstrap-") {
			return sshv2.ExecResult{Stdout: []byte("ok")}, nil
		}
		return sshv2.ExecResult{}, nil
	}}
	masterExec := &funcExecutor{fn: func(context.Context, string) (sshv2.ExecResult, error) {
		return sshv2.ExecResult{ExitCode: 1}, errors.New("Process exited with status 1") // no kubeconfig
	}}
	p := ensureTestProvider(t, deps{
		connector:  routeConnector{execs: map[string]*funcExecutor{testMasterIP: masterExec}},
		masterConn: fakeMasterConn{err: errors.New("must not be called for a clean master")},
	})

	if err := p.ensureDhctlBootstrapped(context.Background(), setupExec, dhctlTestDef()); err != nil {
		t.Fatalf("ensureDhctlBootstrapped() error = %v, want fresh bootstrap to succeed", err)
	}

	cmds := setupExec.recorded()
	iRmContainer := indexMatching(cmds, contains("docker rm -f", dhctlContainerName))
	iRun := indexMatching(cmds, contains("docker run", "--name "+dhctlContainerName))
	if iRmContainer == -1 || iRun == -1 {
		t.Fatalf("expected container rm and named docker run, got rm=%d run=%d\n%v", iRmContainer, iRun, cmds)
	}
	if iRmContainer > iRun {
		t.Errorf("leftover container must be removed before docker run: rm=%d run=%d\n%v", iRmContainer, iRun, cmds)
	}
}

func TestEnsureDhctlBootstrappedWaitsForInFlightContainer(t *testing.T) {
	t.Parallel()

	setupExec := &funcExecutor{fn: func(_ context.Context, cmd string) (sshv2.ExecResult, error) {
		switch {
		case strings.Contains(cmd, "docker inspect"):
			return sshv2.ExecResult{Stdout: []byte("running\n")}, nil
		case strings.Contains(cmd, "docker wait"):
			return sshv2.ExecResult{Stdout: []byte("0\n")}, nil
		default:
			return sshv2.ExecResult{}, nil
		}
	}}
	masterExec := &funcExecutor{} // after the wait the master has a kubeconfig
	p := ensureTestProvider(t, deps{
		connector:    routeConnector{execs: map[string]*funcExecutor{testMasterIP: masterExec}},
		masterConn:   fakeMasterConn{},
		installReady: func(context.Context, *rest.Config, time.Duration) error { return nil },
	})

	if err := p.ensureDhctlBootstrapped(context.Background(), setupExec, dhctlTestDef()); err != nil {
		t.Fatalf("ensureDhctlBootstrapped() error = %v, want nil", err)
	}

	cmds := setupExec.recorded()
	if indexMatching(cmds, contains("docker wait", dhctlContainerName)) == -1 {
		t.Errorf("expected docker wait on the in-flight container: %v", cmds)
	}
	if indexMatching(cmds, contains("docker run")) != -1 {
		t.Errorf("a second bootstrap must not start while waiting out an in-flight one: %v", cmds)
	}
}

func TestEnsureDhctlBootstrappedHalfBootstrappedMaster(t *testing.T) {
	t.Parallel()

	setupExec := &funcExecutor{}
	masterExec := &funcExecutor{} // kubeconfig present
	p := ensureTestProvider(t, deps{
		connector:    routeConnector{execs: map[string]*funcExecutor{testMasterIP: masterExec}},
		masterConn:   fakeMasterConn{},
		installReady: func(context.Context, *rest.Config, time.Duration) error { return errors.New("never became healthy") },
	})

	err := p.ensureDhctlBootstrapped(context.Background(), setupExec, dhctlTestDef())
	if err == nil {
		t.Fatal("ensureDhctlBootstrapped() error = nil, want half-bootstrapped error")
	}
	if !strings.Contains(err.Error(), "half-bootstrapped") {
		t.Errorf("error should explain the half-bootstrapped state, got: %v", err)
	}
	if indexMatching(setupExec.recorded(), contains("docker run")) != -1 {
		t.Errorf("dhctl must not run over a half-bootstrapped master: %v", setupExec.recorded())
	}
}
