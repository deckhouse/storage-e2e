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
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/deckhouse/virtualization/api/core/v1alpha2"
	"github.com/deckhouse/virtualization/api/core/v1alpha3"

	"github.com/deckhouse/storage-e2e/internal/config"
)

// maxConcurrentOps caps how many resource operations (image/disk/VM create or
// delete waits) run concurrently within a single provisioning phase, so large
// clusters do not open an unbounded number of API calls / tunnel streams at once.
const maxConcurrentOps = 8

type Config struct {
	Namespace    string
	StorageClass string
	SSHPublicKey string

	VMClassName        string
	DefaultVMClassName string

	Timeouts Timeouts
}

type Timeouts struct {
	PollInterval                    time.Duration
	ClusterVirtualImageReadyTimeout time.Duration
	VMClassReadyTimeout             time.Duration
	VMRunningTimeout                time.Duration
	DeleteTimeout                   time.Duration
}

type Provisioner struct {
	client Client
	log    *slog.Logger
	cfg    Config
}

func NewProvisioner(client Client, log *slog.Logger, cfg Config) *Provisioner {
	return &Provisioner{client: client, log: log, cfg: cfg}
}

type plannedVM struct {
	node             *config.ClusterNode
	withStorageTools bool
	withDocker       bool
	cviName          string
}

func (p *Provisioner) plan(def *config.ClusterDefinition) []plannedVM {
	var planned []plannedVM

	add := func(n *config.ClusterNode) {
		if n == nil || n.HostType != config.HostTypeVM {
			return
		}
		planned = append(planned, plannedVM{
			node:             n,
			withStorageTools: n.Role != config.ClusterRoleSetup,
			withDocker:       n.Role == config.ClusterRoleSetup,
			cviName:          cviNameFromImageURL(n.OSType.ImageURL),
		})
	}

	for i := range def.Masters {
		add(&def.Masters[i])
	}
	for i := range def.Workers {
		add(&def.Workers[i])
	}
	add(def.Setup)

	return planned
}

func (p *Provisioner) Provision(ctx context.Context, def *config.ClusterDefinition) error {
	planned := p.plan(def)
	if len(planned) == 0 {
		return fmt.Errorf("no VM nodes to provision")
	}

	if err := p.provisionVMClass(ctx); err != nil {
		return err
	}

	if err := p.provisionClusterVirtualImages(ctx, planned); err != nil {
		return err
	}

	if err := p.provisionDisksAndVMs(ctx, planned); err != nil {
		return err
	}

	if err := p.waitRunningAndCollectIPs(ctx, planned); err != nil {
		return err
	}

	p.log.Info("all VMs are running", "count", len(planned))
	return nil
}

func (p *Provisioner) provisionVMClass(ctx context.Context) error {
	name := p.cfg.VMClassName

	_, err := p.client.GetVirtualMachineClass(ctx, name)
	switch {
	case err == nil:
		return p.waitVMClassReady(ctx, name)
	case !apierrors.IsNotFound(err):
		return fmt.Errorf("get VirtualMachineClass %q: %w", name, err)
	case name == p.cfg.DefaultVMClassName:
		return fmt.Errorf("VirtualMachineClass %q not found on the base cluster; "+
			"enable the virtualization module and ensure this class exists before running tests", name)
	}

	template, err := p.client.GetVirtualMachineClass(ctx, p.cfg.DefaultVMClassName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("VirtualMachineClass %q is missing and template class %q was not found; "+
				"cannot auto-create the class", name, p.cfg.DefaultVMClassName)
		}
		return fmt.Errorf("get template VirtualMachineClass %q: %w", p.cfg.DefaultVMClassName, err)
	}

	class := buildVirtualMachineClass(name, template.Spec, managedLabels())
	if err := createIfAbsentVirtualMachineClass(ctx, p.client, class); err != nil {
		return err
	}
	p.log.Info("created VirtualMachineClass from template",
		"name", name, "template", p.cfg.DefaultVMClassName, "cpuType", "Host")

	return p.waitVMClassReady(ctx, name)
}

func (p *Provisioner) waitVMClassReady(ctx context.Context, name string) error {
	waitCtx, cancel := context.WithTimeout(ctx, p.cfg.Timeouts.VMClassReadyTimeout)
	defer cancel()

	err := waitForCondition(waitCtx, p.cfg.Timeouts.PollInterval,
		func(ctx context.Context) (*v1alpha3.VirtualMachineClass, error) {
			return p.client.GetVirtualMachineClass(ctx, name)
		},
		virtualMachineClassReady,
	)
	if err != nil {
		return fmt.Errorf("wait for VirtualMachineClass %q to become Ready: %w", name, err)
	}
	return nil
}

func (p *Provisioner) provisionClusterVirtualImages(ctx context.Context, planned []plannedVM) error {
	images := uniqueImages(planned)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentOps)
	for name, url := range images {
		g.Go(func() error {
			cvi := buildClusterVirtualImage(name, url, managedLabels())
			if err := createIfAbsentClusterVirtualImage(gctx, p.client, cvi); err != nil {
				return err
			}
			p.log.Info("ensured ClusterVirtualImage, waiting for Ready", "name", name)

			waitCtx, cancel := context.WithTimeout(gctx, p.cfg.Timeouts.ClusterVirtualImageReadyTimeout)
			defer cancel()
			if err := waitForCondition(waitCtx, p.cfg.Timeouts.PollInterval,
				func(ctx context.Context) (*v1alpha2.ClusterVirtualImage, error) {
					return p.client.GetClusterVirtualImage(ctx, name)
				},
				clusterVirtualImageReady,
			); err != nil {
				return fmt.Errorf("wait for ClusterVirtualImage %q to become Ready: %w", name, err)
			}
			p.log.Info("ClusterVirtualImage is Ready", "name", name)
			return nil
		})
	}
	return g.Wait()
}

func (p *Provisioner) provisionDisksAndVMs(ctx context.Context, planned []plannedVM) error {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentOps)
	for _, pl := range planned {
		g.Go(func() error {
			return p.createDiskAndVM(gctx, pl)
		})
	}
	return g.Wait()
}

func (p *Provisioner) createDiskAndVM(ctx context.Context, pl plannedVM) error {
	labels := managedLabels()
	diskName := systemDiskName(pl.node.Hostname)

	vd, err := buildVirtualDisk(p.cfg.Namespace, diskName, pl.cviName, p.cfg.StorageClass, pl.node.DiskSize, labels)
	if err != nil {
		return err
	}
	if err = createIfAbsentVirtualDisk(ctx, p.client, vd); err != nil {
		return err
	}

	cloudInit := buildCloudInit(cloudInitOptions{
		hostname:         pl.node.Hostname,
		sshAuthorizedKey: p.cfg.SSHPublicKey,
		withStorageTools: pl.withStorageTools,
		withDocker:       pl.withDocker,
	})

	machine, err := buildVirtualMachine(vmParams{
		Namespace:    p.cfg.Namespace,
		Name:         pl.node.Hostname,
		VMClassName:  p.cfg.VMClassName,
		DiskName:     diskName,
		CloudInit:    cloudInit,
		CPU:          pl.node.CPU,
		RAMGi:        pl.node.RAM,
		CoreFraction: pl.node.CoreFraction,
		Labels:       labels,
	})
	if err != nil {
		return err
	}
	if err := createIfAbsentVirtualMachine(ctx, p.client, machine); err != nil {
		return err
	}
	p.log.Info("ensured VirtualDisk and VirtualMachine", "vm", pl.node.Hostname)
	return nil
}

func (p *Provisioner) waitRunningAndCollectIPs(ctx context.Context, planned []plannedVM) error {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentOps)
	for _, pl := range planned {
		g.Go(func() error {
			waitCtx, cancel := context.WithTimeout(gctx, p.cfg.Timeouts.VMRunningTimeout)
			defer cancel()

			var ip string
			err := waitForCondition(waitCtx, p.cfg.Timeouts.PollInterval,
				func(ctx context.Context) (*v1alpha2.VirtualMachine, error) {
					return p.client.GetVirtualMachine(ctx, p.cfg.Namespace, pl.node.Hostname)
				},
				func(machine *v1alpha2.VirtualMachine, getErr error) (bool, error) {
					done, condErr := virtualMachineRunning(machine, getErr)
					if done {
						ip = machine.Status.IPAddress
					}
					return done, condErr
				},
			)
			if err != nil {
				return fmt.Errorf("wait for VM %q to become Running: %w", pl.node.Hostname, err)
			}
			pl.node.IPAddress = ip
			p.log.Info("VM is running", "vm", pl.node.Hostname, "ip", ip)
			return nil
		})
	}
	return g.Wait()
}

// uniqueImages maps each distinct CVI name to its source image URL. Because
// cviNameFromImageURL appends a hash of the full URL, the name is unique per
// URL, so distinct images can no longer collide onto the same map key.
func uniqueImages(planned []plannedVM) map[string]string {
	images := make(map[string]string, len(planned))
	for _, pl := range planned {
		images[pl.cviName] = pl.node.OSType.ImageURL
	}
	return images
}

func (p *Provisioner) Teardown(ctx context.Context) error {
	if err := p.teardownVirtualMachines(ctx); err != nil {
		return err
	}
	return p.teardownVirtualDisks(ctx)
}

func (p *Provisioner) teardownVirtualMachines(ctx context.Context) error {
	vms, err := p.client.ListVirtualMachines(ctx, p.cfg.Namespace)
	if err != nil {
		return fmt.Errorf("list VirtualMachines: %w", err)
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentOps)
	for i := range vms {
		machine := vms[i]
		if !isManaged(machine.ObjectMeta) {
			continue
		}
		g.Go(func() error {
			p.log.Info("deleting VirtualMachine", "vm", machine.Name)
			if err := p.client.DeleteVirtualMachine(gctx, machine.Namespace, machine.Name); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("delete VirtualMachine %s/%s: %w", machine.Namespace, machine.Name, err)
			}
			return waitDeleted(gctx, p.cfg.Timeouts.PollInterval, p.cfg.Timeouts.DeleteTimeout,
				func(ctx context.Context) (*v1alpha2.VirtualMachine, error) {
					return p.client.GetVirtualMachine(ctx, machine.Namespace, machine.Name)
				}, "VirtualMachine", machine.Name)
		})
	}
	return g.Wait()
}

func (p *Provisioner) teardownVirtualDisks(ctx context.Context) error {
	vds, err := p.client.ListVirtualDisks(ctx, p.cfg.Namespace)
	if err != nil {
		return fmt.Errorf("list VirtualDisks: %w", err)
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentOps)
	for i := range vds {
		vd := vds[i]
		if !isManaged(vd.ObjectMeta) {
			continue
		}
		g.Go(func() error {
			p.log.Info("deleting VirtualDisk", "vd", vd.Name)
			if err := p.client.DeleteVirtualDisk(gctx, vd.Namespace, vd.Name); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("delete VirtualDisk %s/%s: %w", vd.Namespace, vd.Name, err)
			}
			return waitDeleted(gctx, p.cfg.Timeouts.PollInterval, p.cfg.Timeouts.DeleteTimeout,
				func(ctx context.Context) (*v1alpha2.VirtualDisk, error) {
					return p.client.GetVirtualDisk(ctx, vd.Namespace, vd.Name)
				}, "VirtualDisk", vd.Name)
		})
	}
	return g.Wait()
}

func waitDeleted[T any](ctx context.Context, interval, timeout time.Duration, get func(context.Context) (T, error), kind, name string) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := waitForCondition(waitCtx, interval, get, resourceDeleted[T]); err != nil {
		return fmt.Errorf("wait for %s %q deletion: %w", kind, name, err)
	}
	return nil
}
