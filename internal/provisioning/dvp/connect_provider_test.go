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
	"slices"
	"testing"

	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
)

type fakeResolver struct {
	rec *recorder
	ip  string
	err error
}

func (f fakeResolver) resolveMasterIP(ctx context.Context, baseKube *rest.Config, namespace, hostname string) (string, error) {
	f.rec.log("resolve:" + namespace + "/" + hostname)
	if f.err != nil {
		return "", f.err
	}
	return f.ip, nil
}

type fakeMaster struct {
	rec *recorder
	cfg *rest.Config
	err error
}

func (f fakeMaster) connectToMaster(ctx context.Context, masterIP string) (*rest.Config, func(), error) {
	f.rec.log("connectToMaster:" + masterIP)
	if f.err != nil {
		return nil, nil, f.err
	}
	return f.cfg, func() { f.rec.log("masterCleanup") }, nil
}

func newConnectProvider(t *testing.T, conn fakeConnector, resolver fakeResolver, master fakeMaster) *dvpProvider {
	t.Helper()
	cfg := &clusterprovider.ClusterConfig{ClusterBootstrapConfigPath: writeClusterConfig(t)}
	dvpConf := &Config{Namespace: "e2e-test"}
	creds := Credentials{SSHKey: testPrivateKeyPEM(t)}
	return newProvider(quietLogger(), cfg, dvpConf, creds, deps{
		connector:  conn,
		resolver:   resolver,
		masterConn: master,
	})
}

func TestConnectHappyPath(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	wantCfg := &rest.Config{Host: "https://master.local"}

	p := newConnectProvider(t,
		fakeConnector{rec: rec},
		fakeResolver{rec: rec, ip: "10.10.10.5"},
		fakeMaster{rec: rec, cfg: wantCfg},
	)

	gotCfg, cleanup, err := p.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect: unexpected error: %v", err)
	}
	if gotCfg != wantCfg {
		t.Fatalf("Connect returned rest.Config %v, want %v", gotCfg, wantCfg)
	}
	cleanup()

	// Base connection is resolved and released BEFORE connecting to the master
	// (connectToMaster opens its own SSH tunnel to the resolved IP).
	want := []string{"connect", "resolve:e2e-test/master-1", "cleanup", "connectToMaster:10.10.10.5", "masterCleanup"}
	if !slices.Equal(rec.calls, want) {
		t.Fatalf("call order = %v, want %v", rec.calls, want)
	}
}

func TestConnectResolveError(t *testing.T) {
	t.Parallel()
	rec := &recorder{}

	p := newConnectProvider(t,
		fakeConnector{rec: rec},
		fakeResolver{rec: rec, err: errors.New("vm not found")},
		fakeMaster{rec: rec, cfg: &rest.Config{}},
	)

	if _, _, err := p.Connect(context.Background()); err == nil {
		t.Fatal("Connect: expected error when master IP resolution fails")
	}
	// The base connection must still be released, and connectToMaster must NOT run.
	if slices.Contains(rec.calls, "connectToMaster:10.10.10.5") {
		t.Fatalf("connectToMaster ran despite resolve error: %v", rec.calls)
	}
	if !slices.Contains(rec.calls, "cleanup") {
		t.Fatalf("base connection was not released on resolve error: %v", rec.calls)
	}
}

func TestFirstMasterHostname(t *testing.T) {
	t.Parallel()

	if _, err := firstMasterHostname(nil); err == nil {
		t.Fatal("expected error for nil definition")
	}

	def := &config.ClusterDefinition{}
	if _, err := firstMasterHostname(def); err == nil {
		t.Fatal("expected error when no master VM is present")
	}

	def.Masters = []config.ClusterNode{
		{Hostname: "master-1", HostType: config.HostTypeVM},
	}
	got, err := firstMasterHostname(def)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "master-1" {
		t.Fatalf("hostname = %q, want master-1", got)
	}
}
