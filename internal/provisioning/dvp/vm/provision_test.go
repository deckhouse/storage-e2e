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

package vm

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/virtualization/api/core/v1alpha2"
	"github.com/deckhouse/virtualization/api/core/v1alpha3"

	"github.com/deckhouse/storage-e2e/internal/config"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testConfig() Config {
	return Config{
		Namespace:          "ns",
		StorageClass:       "sc",
		SSHPublicKey:       "ssh-ed25519 AAAA test",
		VMClassName:        "generic",
		DefaultVMClassName: "generic",
		Timeouts: Timeouts{
			PollInterval:                    time.Millisecond,
			ClusterVirtualImageReadyTimeout: time.Minute,
			VMClassReadyTimeout:             time.Minute,
			VMRunningTimeout:                2 * time.Second,
			DeleteTimeout:                   2 * time.Second,
		},
	}
}

func readyVMClass(name string) *v1alpha3.VirtualMachineClass {
	class := &v1alpha3.VirtualMachineClass{ObjectMeta: metav1.ObjectMeta{Name: name}}
	class.Status.Phase = v1alpha3.ClassPhaseReady
	return class
}

func vmNode(hostname, imageURL string) config.ClusterNode {
	return config.ClusterNode{
		Hostname: hostname,
		HostType: config.HostTypeVM,
		OSType:   config.OSType{ImageURL: imageURL},
		CPU:      2,
		RAM:      4,
		DiskSize: 20,
	}
}

func TestProvisionHappyPath(t *testing.T) {
	c := newFakeClient()
	c.seedVMClass(readyVMClass("generic"))
	c.onGetCVI = func(cvi *v1alpha2.ClusterVirtualImage) { cvi.Status.Phase = v1alpha2.ImageReady }
	c.onGetVM = func(machine *v1alpha2.VirtualMachine) {
		machine.Status.Phase = v1alpha2.MachineRunning
		machine.Status.IPAddress = "10.0.0.5"
	}

	def := &config.ClusterDefinition{
		Masters: []config.ClusterNode{vmNode("master-1", "http://example/os-a.qcow2")},
		Workers: []config.ClusterNode{vmNode("worker-1", "http://example/os-a.qcow2")},
	}

	p := NewProvisioner(c, testLogger(), testConfig())
	if err := p.Provision(context.Background(), def); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	if def.Masters[0].IPAddress != "10.0.0.5" || def.Workers[0].IPAddress != "10.0.0.5" {
		t.Errorf("IPs not written back: master=%q worker=%q", def.Masters[0].IPAddress, def.Workers[0].IPAddress)
	}
	if def.Setup != nil {
		t.Errorf("def.Setup = %+v, want nil (no implicit setup VM)", def.Setup)
	}

	if got := c.createCount("vm"); got != 2 {
		t.Errorf("VMs created = %d, want 2 (master+worker)", got)
	}
	if got := c.createCount("vd"); got != 2 {
		t.Errorf("VDs created = %d, want 2", got)
	}
	// One unique image shared by master+worker.
	if got := c.createCount("cvi"); got != 1 {
		t.Errorf("CVIs created = %d, want 1", got)
	}
}

func TestProvisionIncludesDeclaredSetupNode(t *testing.T) {
	c := newFakeClient()
	c.seedVMClass(readyVMClass("generic"))
	c.onGetCVI = func(cvi *v1alpha2.ClusterVirtualImage) { cvi.Status.Phase = v1alpha2.ImageReady }
	c.onGetVM = func(machine *v1alpha2.VirtualMachine) {
		machine.Status.Phase = v1alpha2.MachineRunning
		machine.Status.IPAddress = "10.0.0.7"
	}

	setup := vmNode("bootstrap-1", "http://example/os-b.qcow2")
	setup.Role = config.ClusterRoleSetup
	def := &config.ClusterDefinition{
		Masters: []config.ClusterNode{vmNode("master-1", "http://example/os-a.qcow2")},
		Setup:   &setup,
	}

	p := NewProvisioner(c, testLogger(), testConfig())
	if err := p.Provision(context.Background(), def); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	if got := c.createCount("vm"); got != 2 {
		t.Errorf("VMs created = %d, want 2 (master+setup)", got)
	}
	machine, err := c.GetVirtualMachine(context.Background(), "ns", "bootstrap-1")
	if err != nil {
		t.Fatalf("get setup VM: %v", err)
	}
	if !strings.Contains(machine.Spec.Provisioning.UserData, "docker.io") {
		t.Error("setup node cloud-init missing docker.io (Docker profile not applied)")
	}
	master, err := c.GetVirtualMachine(context.Background(), "ns", "master-1")
	if err != nil {
		t.Fatalf("get master VM: %v", err)
	}
	if strings.Contains(master.Spec.Provisioning.UserData, "docker.io") {
		t.Error("master cloud-init has docker.io (Docker profile leaked onto a cluster node)")
	}
	if def.Setup == nil || def.Setup.IPAddress != "10.0.0.7" {
		t.Errorf("def.Setup = %+v, want IP 10.0.0.7", def.Setup)
	}
}

func TestProvisionMissingDefaultVMClassFailsFast(t *testing.T) {
	c := newFakeClient() // no "generic" class seeded
	def := &config.ClusterDefinition{
		Masters: []config.ClusterNode{vmNode("master-1", "http://example/os-a.qcow2")},
	}

	p := NewProvisioner(c, testLogger(), testConfig())
	if err := p.Provision(context.Background(), def); err == nil {
		t.Fatal("expected error for missing default VirtualMachineClass, got nil")
	}
}

func TestProvisionFailFastOnDegradedVM(t *testing.T) {
	c := newFakeClient()
	c.seedVMClass(readyVMClass("generic"))
	c.onGetCVI = func(cvi *v1alpha2.ClusterVirtualImage) { cvi.Status.Phase = v1alpha2.ImageReady }
	c.onGetVM = func(machine *v1alpha2.VirtualMachine) {
		if strings.Contains(machine.Name, "bad") {
			machine.Status.Phase = v1alpha2.MachineDegraded
		}
		// other VMs stay Pending forever, exercising fail-fast cancellation.
	}

	def := &config.ClusterDefinition{
		Masters: []config.ClusterNode{
			vmNode("good-1", "http://example/os-a.qcow2"),
			vmNode("bad-1", "http://example/os-a.qcow2"),
		},
	}

	p := NewProvisioner(c, testLogger(), testConfig())

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		done <- p.Provision(context.Background(), def)
	}()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "degraded") {
			t.Fatalf("err = %v, want a degraded error", err)
		}
		if elapsed := time.Since(start); elapsed >= 2*time.Second {
			t.Errorf("Provision took %v, want fast cancellation (< VMRunningTimeout)", elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Provision hung: fail-fast did not cancel sibling waits")
	}
}

func TestTeardownIdempotent(t *testing.T) {
	c := newFakeClient()
	c.seedVMClass(readyVMClass("generic"))
	c.onGetCVI = func(cvi *v1alpha2.ClusterVirtualImage) { cvi.Status.Phase = v1alpha2.ImageReady }
	c.onGetVM = func(machine *v1alpha2.VirtualMachine) {
		machine.Status.Phase = v1alpha2.MachineRunning
		machine.Status.IPAddress = "10.0.0.5"
	}

	def := &config.ClusterDefinition{
		Masters: []config.ClusterNode{vmNode("master-1", "http://example/os-a.qcow2")},
	}

	p := NewProvisioner(c, testLogger(), testConfig())
	if err := p.Provision(context.Background(), def); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	if err := p.Teardown(context.Background()); err != nil {
		t.Fatalf("Teardown: %v", err)
	}

	vms, _ := c.ListVirtualMachines(context.Background(), "ns")
	vds, _ := c.ListVirtualDisks(context.Background(), "ns")
	cvis, _ := c.ListClusterVirtualImages(context.Background())
	if len(vms) != 0 || len(vds) != 0 {
		t.Errorf("after teardown: vms=%d vds=%d, want 0/0", len(vms), len(vds))
	}
	if len(cvis) == 0 {
		t.Error("after teardown: cvis=0, want CVIs kept as cache")
	}
	if _, err := c.GetVirtualMachineClass(context.Background(), "generic"); err != nil {
		t.Errorf("VirtualMachineClass should survive teardown: %v", err)
	}

	// Second teardown must be a no-op, not an error.
	if err := p.Teardown(context.Background()); err != nil {
		t.Fatalf("second Teardown should be idempotent: %v", err)
	}
}
