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
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/config"
	sshv2 "github.com/deckhouse/storage-e2e/internal/infrastructure/ssh/v2"
	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
)

const testClusterYAML = `clusterDefinition:
  masters:
    - hostname: "master-1"
      hostType: "vm"
      osType: "Ubuntu 22.04 6.2.0-39-generic"
      cpu: 4
      ram: 8
      diskSize: 30
  workers:
    - hostname: "worker-1"
      hostType: "vm"
      osType: "Ubuntu 22.04 6.2.0-39-generic"
      cpu: 2
      ram: 8
      diskSize: 20
  dkpParameters:
    kubernetesVersion: "Automatic"
    podSubnetCIDR: "10.112.0.0/16"
    serviceSubnetCIDR: "10.225.0.0/16"
    clusterDomain: "cluster.local"
    registryRepo: "dev-registry.deckhouse.io/sys/deckhouse-oss"
`

func writeClusterConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "cluster_config.yml")
	if err := os.WriteFile(path, []byte(testClusterYAML), 0o600); err != nil {
		t.Fatalf("write cluster config: %v", err)
	}
	return path
}

func testPrivateKeyPEM(t *testing.T) []byte {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return pem.EncodeToMemory(block)
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// recorder collects the ordered collaborator calls and the ssh keys handed to
// the fleet factory, so every fake can share a single source of truth.
type recorder struct {
	calls []string
	keys  []string
}

func (r *recorder) log(call string) { r.calls = append(r.calls, call) }

// --- fakes ---

type fakeConnector struct {
	rec       *recorder
	err       error
	vmExecErr error
}

func (f fakeConnector) Connect(ctx context.Context) (*rest.Config, func(), error) {
	f.rec.log("connect")
	if f.err != nil {
		return nil, nil, f.err
	}
	return &rest.Config{}, func() { f.rec.log("cleanup") }, nil
}

func (f fakeConnector) VMExecutor(ctx context.Context, vmIP string) (remoteExecutor, func(), error) {
	f.rec.log("vmexec")
	if f.vmExecErr != nil {
		return nil, nil, f.vmExecErr
	}
	return fakeExecutor{rec: f.rec}, func() { f.rec.log("closeExec") }, nil
}

// fakeExecutor reports Docker ready on the first poll (exit code 0).
type fakeExecutor struct{ rec *recorder }

func (f fakeExecutor) Exec(ctx context.Context, cmd string) (sshv2.ExecResult, error) {
	return sshv2.ExecResult{}, nil
}

type fakeKube struct {
	rec          *recorder
	reachableErr error
	moduleErr    error
	namespaceErr error
}

func (f fakeKube) CheckReachable(ctx context.Context, kube *rest.Config) error {
	f.rec.log("reachable")
	return f.reachableErr
}

func (f fakeKube) WaitModuleReady(ctx context.Context, kube *rest.Config, module string, timeout time.Duration) error {
	f.rec.log("module")
	return f.moduleErr
}

func (f fakeKube) EnsureNamespace(ctx context.Context, kube *rest.Config, ns string) error {
	f.rec.log("namespace")
	return f.namespaceErr
}

type fakeFleet struct {
	rec          *recorder
	provisionErr error
	teardownErr  error
}

func (f fakeFleet) Provision(ctx context.Context, def *config.ClusterDefinition) error {
	f.rec.log("provision")
	return f.provisionErr
}

func (f fakeFleet) Teardown(ctx context.Context) error {
	f.rec.log("teardown")
	return f.teardownErr
}

type fakeFleetFactory struct {
	rec   *recorder
	fleet vmFleet
	err   error
}

func (f fakeFleetFactory) New(ctx context.Context, kube *rest.Config, sshPublicKey string) (vmFleet, error) {
	f.rec.log("fleet.New")
	f.rec.keys = append(f.rec.keys, sshPublicKey)
	if f.err != nil {
		return nil, f.err
	}
	return f.fleet, nil
}

func newTestProvider(t *testing.T, conn fakeConnector, kube fakeKube, factory fakeFleetFactory) *dvpProvider {
	t.Helper()
	cfg := &clusterprovider.ClusterConfig{ClusterBootstrapConfigPath: writeClusterConfig(t)}
	dvpConf := &Config{Namespace: "e2e-test"}
	creds := Credentials{SSHKey: testPrivateKeyPEM(t)}
	return newProvider(quietLogger(), cfg, dvpConf, creds, deps{
		connector: conn, kube: kube, fleet: factory,
	})
}

// The tests below target provision — the fakeable provisioning half of
// Bootstrap. The install half (dhctl → connect-to-master → join → modules)
// requires a live cluster and is intentionally not faked (implementation focus,
// per the design doc). cleanups.run() stands in for Bootstrap's deferred
// cleanup so the released-resource ordering stays observable.

func TestProvisionHappyPath(t *testing.T) {
	t.Parallel()
	rec := &recorder{}

	conn := fakeConnector{rec: rec}
	kube := fakeKube{rec: rec}
	factory := fakeFleetFactory{rec: rec, fleet: fakeFleet{rec: rec}}

	p := newTestProvider(t, conn, kube, factory)

	cleanups := cleanupStack{}
	if _, _, err := p.provision(context.Background(), &cleanups); err != nil {
		t.Fatalf("provision() error = %v", err)
	}
	cleanups.run()

	want := []string{"connect", "reachable", "module", "namespace", "fleet.New", "provision", "vmexec", "closeExec", "cleanup"}
	if !slices.Equal(rec.calls, want) {
		t.Errorf("call order = %v, want %v", rec.calls, want)
	}
	if len(rec.keys) != 1 || rec.keys[0] == "" {
		t.Errorf("fleet.New got ssh key %q, want non-empty derived key", rec.keys)
	}
}

func TestProvisionConnectErrorShortCircuits(t *testing.T) {
	t.Parallel()
	rec := &recorder{}

	conn := fakeConnector{rec: rec, err: errors.New("dial fail")}
	kube := fakeKube{rec: rec}
	factory := fakeFleetFactory{rec: rec, fleet: fakeFleet{rec: rec}}

	p := newTestProvider(t, conn, kube, factory)

	cleanups := cleanupStack{}
	if _, _, err := p.provision(context.Background(), &cleanups); err == nil {
		t.Fatal("provision() error = nil, want connect error")
	}
	cleanups.run()

	if !slices.Equal(rec.calls, []string{"connect"}) {
		t.Errorf("calls after connect failure = %v, want [connect]", rec.calls)
	}
	if slices.Contains(rec.calls, "cleanup") {
		t.Error("cleanup ran though connect failed (nothing pushed)")
	}
}

func TestProvisionModuleErrorRunsCleanup(t *testing.T) {
	t.Parallel()
	rec := &recorder{}

	conn := fakeConnector{rec: rec}
	kube := fakeKube{rec: rec, moduleErr: errors.New("not ready")}
	factory := fakeFleetFactory{rec: rec, fleet: fakeFleet{rec: rec}}

	p := newTestProvider(t, conn, kube, factory)

	cleanups := cleanupStack{}
	if _, _, err := p.provision(context.Background(), &cleanups); err == nil {
		t.Fatal("provision() error = nil, want module error")
	}
	cleanups.run()

	if !slices.Contains(rec.calls, "cleanup") {
		t.Error("cleanup did not run after module failure")
	}
	if slices.Contains(rec.calls, "provision") {
		t.Error("provision ran despite module failure")
	}
}

func TestProvisionFleetError(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	provisionErr := errors.New("boom")

	conn := fakeConnector{rec: rec}
	kube := fakeKube{rec: rec}
	factory := fakeFleetFactory{rec: rec, fleet: fakeFleet{rec: rec, provisionErr: provisionErr}}

	p := newTestProvider(t, conn, kube, factory)

	cleanups := cleanupStack{}
	_, _, err := p.provision(context.Background(), &cleanups)
	if err == nil {
		t.Fatal("provision() error = nil, want provision error")
	}
	if !errors.Is(err, provisionErr) {
		t.Errorf("provision() error = %v, want wrap of %v", err, provisionErr)
	}
	cleanups.run()

	if !slices.Contains(rec.calls, "cleanup") {
		t.Error("cleanup did not run after provision failure")
	}
}

func TestRemoveTearsDownAndCleans(t *testing.T) {
	t.Parallel()
	rec := &recorder{}

	conn := fakeConnector{rec: rec}
	kube := fakeKube{rec: rec}
	factory := fakeFleetFactory{rec: rec, fleet: fakeFleet{rec: rec}}

	p := newTestProvider(t, conn, kube, factory)

	if err := p.Remove(context.Background()); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	want := []string{"connect", "fleet.New", "teardown", "cleanup"}
	if !slices.Equal(rec.calls, want) {
		t.Errorf("call order = %v, want %v", rec.calls, want)
	}
	if len(rec.keys) != 1 || rec.keys[0] != "" {
		t.Errorf("Remove fleet.New ssh key = %q, want empty", rec.keys)
	}
}

func TestRemoveTeardownError(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	teardownErr := errors.New("stuck")

	conn := fakeConnector{rec: rec}
	kube := fakeKube{rec: rec}
	factory := fakeFleetFactory{rec: rec, fleet: fakeFleet{rec: rec, teardownErr: teardownErr}}

	p := newTestProvider(t, conn, kube, factory)

	err := p.Remove(context.Background())
	if err == nil {
		t.Fatal("Remove() error = nil, want teardown error")
	}
	if !errors.Is(err, teardownErr) {
		t.Errorf("Remove() error = %v, want wrap of %v", err, teardownErr)
	}
	if !slices.Contains(rec.calls, "cleanup") {
		t.Error("cleanup did not run after teardown failure")
	}
}
