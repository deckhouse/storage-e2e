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
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/config"
	sshv2 "github.com/deckhouse/storage-e2e/internal/infrastructure/ssh/v2"
	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
)

func TestBuildNodeBootstrapCommand(t *testing.T) {
	t.Parallel()
	const want = "sudo bash <<'BOOTSTRAP_EOF'\n#!/bin/bash\necho hi\nBOOTSTRAP_EOF"
	if got := buildNodeBootstrapCommand("#!/bin/bash\necho hi"); got != want {
		t.Errorf("buildNodeBootstrapCommand()\n got = %q\nwant = %q", got, want)
	}
}

func TestIsRetryableJoinError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		stdout string
		stderr string
		err    error
		want   bool
	}{
		{"nil error never retries", "HTTP Error 401", "", nil, false},
		{"401 in stdout", "kubeadm join ... HTTP Error 401 ...", "", errors.New("exit 1"), true},
		{"unauthorized in stderr", "", "server responded Unauthorized", errors.New("exit 1"), true},
		{"connection refused in stdout", "dial tcp: Connection refused", "", errors.New("exit 1"), true},
		{"unrelated failure not retried", "some other failure", "boom", errors.New("exit 1"), false},
		{"empty output not retried", "", "", errors.New("exit 1"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := sshv2.ExecResult{Stdout: []byte(tt.stdout), Stderr: []byte(tt.stderr), ExitCode: 1}
			if got := isRetryableJoinError(res, tt.err); got != tt.want {
				t.Errorf("isRetryableJoinError(%q/%q) = %v, want %v", tt.stdout, tt.stderr, got, tt.want)
			}
		})
	}
}

// joinConnector is a baseConnector whose VMExecutor hands out a per-IP executor
// and records which IPs were connected.
type joinConnector struct {
	mu      sync.Mutex
	ips     []string
	execFor map[string]*funcExecutor
	execErr error
}

func (c *joinConnector) Connect(ctx context.Context) (*rest.Config, func(), error) {
	return &rest.Config{}, func() {}, nil
}

func (c *joinConnector) VMExecutor(ctx context.Context, ip string) (remoteExecutor, func(), error) {
	c.mu.Lock()
	c.ips = append(c.ips, ip)
	c.mu.Unlock()
	if c.execErr != nil {
		return nil, nil, c.execErr
	}
	return c.execFor[ip], func() {}, nil
}

func (c *joinConnector) connectedIPs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.ips...)
}

func bootstrapSecretsClientset(extra ...runtime.Object) k8s.Interface {
	newSecret := func(name string) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: bootstrapSecretNamespace, Name: name},
			Data:       map[string][]byte{bootstrapScriptKey: []byte("#!/bin/bash\ntrue")},
		}
	}
	objs := append([]runtime.Object{newSecret(masterBootstrapSecret), newSecret(workerBootstrapSecret)}, extra...)
	return fake.NewClientset(objs...)
}

func registeredNode(name string) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

func joinTestProvider(t *testing.T, conn baseConnector) *dvpProvider {
	t.Helper()
	return newProvider(quietLogger(), &clusterprovider.ClusterConfig{}, &Config{VMSSHUser: "cloud"}, Credentials{}, deps{connector: conn})
}

func okExecutor() *funcExecutor {
	return &funcExecutor{fn: func(ctx context.Context, cmd string) (sshv2.ExecResult, error) {
		return sshv2.ExecResult{ExitCode: 0}, nil
	}}
}

func TestJoinNodesAllSucceed(t *testing.T) {
	t.Parallel()

	def := &config.ClusterDefinition{
		Masters: []config.ClusterNode{
			{Hostname: "m1", HostType: config.HostTypeVM, IPAddress: "10.10.1.1"},
			{Hostname: "m2", HostType: config.HostTypeVM, IPAddress: "10.10.1.2"},
		},
		Workers: []config.ClusterNode{
			{Hostname: "w1", HostType: config.HostTypeVM, IPAddress: "10.10.1.3"},
			{Hostname: "w2", HostType: config.HostTypeVM, IPAddress: "10.10.1.4"},
		},
	}
	conn := &joinConnector{execFor: map[string]*funcExecutor{
		"10.10.1.2": okExecutor(), "10.10.1.3": okExecutor(), "10.10.1.4": okExecutor(),
	}}
	p := joinTestProvider(t, conn)

	if err := p.joinNodesWithClient(context.Background(), bootstrapSecretsClientset(), def); err != nil {
		t.Fatalf("joinNodesWithClient() error = %v", err)
	}

	got := conn.connectedIPs()
	want := map[string]bool{"10.10.1.2": true, "10.10.1.3": true, "10.10.1.4": true}
	if len(got) != len(want) {
		t.Fatalf("connected IPs = %v, want the 3 extra-master/worker IPs", got)
	}
	for _, ip := range got {
		if !want[ip] {
			t.Errorf("unexpected join to %s (first master must be skipped)", ip)
		}
	}
	if strings.Contains(strings.Join(got, ","), "10.10.1.1") {
		t.Error("first master 10.10.1.1 should not be joined")
	}
}

func TestJoinNodesSkipsAlreadyRegisteredNodes(t *testing.T) {
	t.Parallel()

	def := &config.ClusterDefinition{
		Masters: []config.ClusterNode{
			{Hostname: "m1", HostType: config.HostTypeVM, IPAddress: "10.10.1.1"},
			{Hostname: "m2", HostType: config.HostTypeVM, IPAddress: "10.10.1.2"},
		},
		Workers: []config.ClusterNode{
			{Hostname: "w1", HostType: config.HostTypeVM, IPAddress: "10.10.1.3"},
			{Hostname: "w2", HostType: config.HostTypeVM, IPAddress: "10.10.1.4"},
		},
	}
	// m2 and w1 already joined by a previous (interrupted) run.
	cs := bootstrapSecretsClientset(registeredNode("m2"), registeredNode("w1"))
	conn := &joinConnector{execFor: map[string]*funcExecutor{"10.10.1.4": okExecutor()}}
	p := joinTestProvider(t, conn)

	if err := p.joinNodesWithClient(context.Background(), cs, def); err != nil {
		t.Fatalf("joinNodesWithClient() error = %v", err)
	}
	if got := conn.connectedIPs(); len(got) != 1 || got[0] != "10.10.1.4" {
		t.Errorf("connected IPs = %v, want only 10.10.1.4 (w2)", got)
	}
}

func TestJoinNodesAllRegisteredSkipsScriptFetch(t *testing.T) {
	t.Parallel()

	def := &config.ClusterDefinition{
		Masters: []config.ClusterNode{
			{Hostname: "m1", HostType: config.HostTypeVM, IPAddress: "10.10.1.1"},
			{Hostname: "m2", HostType: config.HostTypeVM, IPAddress: "10.10.1.2"},
		},
		Workers: []config.ClusterNode{
			{Hostname: "w1", HostType: config.HostTypeVM, IPAddress: "10.10.1.3"},
		},
	}
	// All extra nodes registered; no bootstrap secrets in the clientset — the
	// join must succeed without ever fetching a script.
	cs := fake.NewClientset(registeredNode("m2"), registeredNode("w1"))
	conn := &joinConnector{execFor: map[string]*funcExecutor{}}
	p := joinTestProvider(t, conn)

	if err := p.joinNodesWithClient(context.Background(), cs, def); err != nil {
		t.Fatalf("joinNodesWithClient() error = %v, want nil (all nodes already joined)", err)
	}
	if ips := conn.connectedIPs(); len(ips) != 0 {
		t.Errorf("no nodes should be joined, connected: %v", ips)
	}
}

func TestJoinNodesZeroExtraNodesNoOp(t *testing.T) {
	t.Parallel()

	def := &config.ClusterDefinition{
		Masters: []config.ClusterNode{{Hostname: "m1", HostType: config.HostTypeVM, IPAddress: "10.10.1.1"}},
	}
	conn := &joinConnector{execFor: map[string]*funcExecutor{}}
	p := joinTestProvider(t, conn)

	// Empty clientset: no secret reads should happen either.
	if err := p.joinNodesWithClient(context.Background(), fake.NewClientset(), def); err != nil {
		t.Fatalf("joinNodesWithClient() error = %v", err)
	}
	if ips := conn.connectedIPs(); len(ips) != 0 {
		t.Errorf("no nodes should be joined, connected: %v", ips)
	}
}

func TestJoinNodesOneFailsOthersCanceled(t *testing.T) {
	t.Parallel()

	var canceled atomic.Int32
	block := func() *funcExecutor {
		return &funcExecutor{fn: func(ctx context.Context, cmd string) (sshv2.ExecResult, error) {
			<-ctx.Done()
			canceled.Add(1)
			return sshv2.ExecResult{}, ctx.Err()
		}}
	}
	failNonRetryable := &funcExecutor{fn: func(ctx context.Context, cmd string) (sshv2.ExecResult, error) {
		return sshv2.ExecResult{Stdout: []byte("fatal: broken script")}, errors.New("exit status 2")
	}}

	def := &config.ClusterDefinition{
		Masters: []config.ClusterNode{
			{Hostname: "m1", HostType: config.HostTypeVM, IPAddress: "10.10.1.1"},
			{Hostname: "m2", HostType: config.HostTypeVM, IPAddress: "10.10.1.2"},
		},
		Workers: []config.ClusterNode{
			{Hostname: "w1", HostType: config.HostTypeVM, IPAddress: "10.10.1.3"},
			{Hostname: "w2", HostType: config.HostTypeVM, IPAddress: "10.10.1.4"},
		},
	}
	conn := &joinConnector{execFor: map[string]*funcExecutor{
		"10.10.1.2": block(),          // extra master, blocks
		"10.10.1.3": failNonRetryable, // worker, fails fast
		"10.10.1.4": block(),          // worker, blocks
	}}
	p := joinTestProvider(t, conn)

	err := p.joinNodesWithClient(context.Background(), bootstrapSecretsClientset(), def)
	if err == nil {
		t.Fatal("joinNodesWithClient() error = nil, want failure")
	}
	if !strings.Contains(err.Error(), "w1") {
		t.Errorf("error should name the failing node w1, got: %v", err)
	}
	if got := canceled.Load(); got != 2 {
		t.Errorf("canceled executors = %d, want 2 (the two blocking nodes)", got)
	}
}

func TestJoinNodeRetryableThenOK(t *testing.T) {
	// Not parallel: shrinks the package-level backoff knobs.
	defer func(d time.Duration) { joinRetryInitialDelay = d }(joinRetryInitialDelay)
	joinRetryInitialDelay = time.Millisecond

	var calls atomic.Int32
	exec := &funcExecutor{fn: func(ctx context.Context, cmd string) (sshv2.ExecResult, error) {
		if calls.Add(1) == 1 {
			return sshv2.ExecResult{Stdout: []byte("HTTP Error 401 Unauthorized"), ExitCode: 1}, errors.New("exit status 1")
		}
		return sshv2.ExecResult{ExitCode: 0}, nil
	}}
	conn := &joinConnector{execFor: map[string]*funcExecutor{"10.10.1.9": exec}}
	p := joinTestProvider(t, conn)

	node := config.ClusterNode{Hostname: "w9", HostType: config.HostTypeVM, IPAddress: "10.10.1.9"}
	if err := p.joinNode(context.Background(), node, "#!/bin/bash\ntrue"); err != nil {
		t.Fatalf("joinNode() error = %v, want success after retry", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("Exec calls = %d, want 2 (retry once then succeed)", got)
	}
}
