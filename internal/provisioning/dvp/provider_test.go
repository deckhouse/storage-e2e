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
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/config"
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

// --- fakes ---

type fakeConnector struct {
	calls      *[]string
	err        error
	cleanupRan *bool
}

func (f fakeConnector) Connect(ctx context.Context) (*rest.Config, func(), error) {
	*f.calls = append(*f.calls, "connect")
	if f.err != nil {
		return nil, nil, f.err
	}
	return &rest.Config{}, func() { *f.cleanupRan = true; *f.calls = append(*f.calls, "cleanup") }, nil
}

type fakeKube struct {
	calls        *[]string
	reachableErr error
	moduleErr    error
	namespaceErr error
}

func (f fakeKube) CheckReachable(ctx context.Context, kube *rest.Config) error {
	*f.calls = append(*f.calls, "reachable")
	return f.reachableErr
}

func (f fakeKube) WaitModuleReady(ctx context.Context, kube *rest.Config, module string, timeout time.Duration) error {
	*f.calls = append(*f.calls, "module")
	return f.moduleErr
}

func (f fakeKube) EnsureNamespace(ctx context.Context, kube *rest.Config, ns string) error {
	*f.calls = append(*f.calls, "namespace")
	return f.namespaceErr
}

type fakeFleet struct {
	calls        *[]string
	provisionErr error
	teardownErr  error
}

func (f fakeFleet) Provision(ctx context.Context, def *config.ClusterDefinition) error {
	*f.calls = append(*f.calls, "provision")
	return f.provisionErr
}

func (f fakeFleet) Teardown(ctx context.Context) error {
	*f.calls = append(*f.calls, "teardown")
	return f.teardownErr
}

type fakeFleetFactory struct {
	calls   *[]string
	fleet   vmFleet
	err     error
	gotKeys *[]string
}

func (f fakeFleetFactory) New(ctx context.Context, kube *rest.Config, sshPublicKey string) (vmFleet, error) {
	*f.calls = append(*f.calls, "fleet.New")
	*f.gotKeys = append(*f.gotKeys, sshPublicKey)
	if f.err != nil {
		return nil, f.err
	}
	return f.fleet, nil
}

func newTestProvider(t *testing.T, calls *[]string, cleanupRan *bool, gotKeys *[]string, conn fakeConnector, kube fakeKube, factory fakeFleetFactory) *dvpProvider {
	t.Helper()
	cfg := &clusterprovider.ClusterConfig{ClusterBootstrapConfigPath: writeClusterConfig(t)}
	dvpConf := &Config{Namespace: "e2e-test"}
	creds := Credentials{SSHKey: testPrivateKeyPEM(t)}
	return newProvider(quietLogger(), cfg, dvpConf, creds, deps{
		connector: conn, kube: kube, fleet: factory,
	})
}

func TestBootstrapHappyPath(t *testing.T) {
	var calls []string
	var cleanupRan bool
	var keys []string

	conn := fakeConnector{calls: &calls, cleanupRan: &cleanupRan}
	kube := fakeKube{calls: &calls}
	factory := fakeFleetFactory{calls: &calls, fleet: fakeFleet{calls: &calls}, gotKeys: &keys}

	p := newTestProvider(t, &calls, &cleanupRan, &keys, conn, kube, factory)

	if err := p.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}

	want := []string{"connect", "reachable", "module", "namespace", "fleet.New", "provision", "cleanup"}
	if got := join(calls); got != join(want) {
		t.Errorf("call order = %v, want %v", calls, want)
	}
	if !cleanupRan {
		t.Error("cleanup did not run")
	}
	if len(keys) != 1 || keys[0] == "" {
		t.Errorf("fleet.New got ssh key %q, want non-empty derived key", keys)
	}
}

func TestBootstrapConnectErrorShortCircuits(t *testing.T) {
	var calls []string
	var cleanupRan bool
	var keys []string

	conn := fakeConnector{calls: &calls, cleanupRan: &cleanupRan, err: errors.New("dial fail")}
	kube := fakeKube{calls: &calls}
	factory := fakeFleetFactory{calls: &calls, fleet: fakeFleet{calls: &calls}, gotKeys: &keys}

	p := newTestProvider(t, &calls, &cleanupRan, &keys, conn, kube, factory)

	if err := p.Bootstrap(context.Background()); err == nil {
		t.Fatal("Bootstrap() error = nil, want connect error")
	}
	if join(calls) != join([]string{"connect"}) {
		t.Errorf("calls after connect failure = %v, want [connect]", calls)
	}
	if cleanupRan {
		t.Error("cleanup ran though connect failed (no cleanup returned)")
	}
}

func TestBootstrapModuleErrorRunsCleanup(t *testing.T) {
	var calls []string
	var cleanupRan bool
	var keys []string

	conn := fakeConnector{calls: &calls, cleanupRan: &cleanupRan}
	kube := fakeKube{calls: &calls, moduleErr: errors.New("not ready")}
	factory := fakeFleetFactory{calls: &calls, fleet: fakeFleet{calls: &calls}, gotKeys: &keys}

	p := newTestProvider(t, &calls, &cleanupRan, &keys, conn, kube, factory)

	if err := p.Bootstrap(context.Background()); err == nil {
		t.Fatal("Bootstrap() error = nil, want module error")
	}
	if !cleanupRan {
		t.Error("cleanup did not run after module failure")
	}
	if contains(calls, "provision") {
		t.Error("provision ran despite module failure")
	}
}

func TestBootstrapProvisionError(t *testing.T) {
	var calls []string
	var cleanupRan bool
	var keys []string

	conn := fakeConnector{calls: &calls, cleanupRan: &cleanupRan}
	kube := fakeKube{calls: &calls}
	factory := fakeFleetFactory{calls: &calls, fleet: fakeFleet{calls: &calls, provisionErr: errors.New("boom")}, gotKeys: &keys}

	p := newTestProvider(t, &calls, &cleanupRan, &keys, conn, kube, factory)

	if err := p.Bootstrap(context.Background()); err == nil {
		t.Fatal("Bootstrap() error = nil, want provision error")
	}
	if !cleanupRan {
		t.Error("cleanup did not run after provision failure")
	}
}

func TestRemoveTearsDownAndCleans(t *testing.T) {
	var calls []string
	var cleanupRan bool
	var keys []string

	conn := fakeConnector{calls: &calls, cleanupRan: &cleanupRan}
	kube := fakeKube{calls: &calls}
	factory := fakeFleetFactory{calls: &calls, fleet: fakeFleet{calls: &calls}, gotKeys: &keys}

	p := newTestProvider(t, &calls, &cleanupRan, &keys, conn, kube, factory)

	if err := p.Remove(context.Background()); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	want := []string{"connect", "fleet.New", "teardown", "cleanup"}
	if join(calls) != join(want) {
		t.Errorf("call order = %v, want %v", calls, want)
	}
	if len(keys) != 1 || keys[0] != "" {
		t.Errorf("Remove fleet.New ssh key = %q, want empty", keys)
	}
}

// helpers

func join(s []string) string {
	out := ""
	for _, v := range s {
		out += v + ","
	}
	return out
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
