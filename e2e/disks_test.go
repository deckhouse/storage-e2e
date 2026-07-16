/*
Copyright 2026 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/rand"

	e2esdk "github.com/deckhouse/storage-e2e/pkg/e2e"
)

// devicePollInterval paces the node-side lsblk checks after attach/detach:
// the DiskManager already waits for the attachment state, this only absorbs
// the guest OS noticing the hotplug.
const devicePollInterval = 5 * time.Second

const deviceWaitTimeout = 5 * time.Minute

// blockDeviceNames returns the names of the node's whole block devices
// (lsblk TYPE=disk, partitions excluded).
func blockDeviceNames(ctx context.Context, nodes e2esdk.NodeExecutor, nodeName string) ([]string, error) {
	res, err := nodes.Exec(ctx, nodeName, "lsblk -dno NAME,TYPE")
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("lsblk exit %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	var names []string
	for line := range strings.Lines(string(res.Stdout)) {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == "disk" {
			names = append(names, fields[0])
		}
	}
	return names, nil
}

// Live checks for the DiskManager capability of the pkg/e2e SDK. Providers
// without disk support skip these specs (CreateDisk reports
// ErrDisksUnsupported).
var _ = Describe("Disk management", func() {
	It("runs the full disk lifecycle on a worker node", Label("disks"), func(ctx SpecContext) {
		if os.Getenv("E2E_TEST_CLUSTER_PROVIDER") == "" {
			Skip("E2E_TEST_CLUSTER_PROVIDER is not set — no provisioned cluster to attach to")
		}

		cl, nodeName := connectAndPickWorker(ctx, "storage-e2e-disk-lifecycle")
		disks := cl.Disks()
		nodes := cl.Nodes()
		diskName := fmt.Sprintf("e2e-disk-%s", rand.String(5))

		By("creating the disk")
		disk, err := disks.CreateDisk(ctx, e2esdk.DiskSpec{Name: diskName, Size: resource.MustParse("1Gi")})
		if errors.Is(err, e2esdk.ErrDisksUnsupported) {
			Skip(fmt.Sprintf("provider %q does not support disk management: %v", cl.ProviderName(), err))
		}
		Expect(err).NotTo(HaveOccurred(), "CreateDisk %s", diskName)
		DeferCleanup(func(ctx SpecContext) {
			// Best effort: the disk may already be detached or gone.
			_ = disks.DetachDisk(ctx, nodeName, diskName)
			_ = disks.DeleteDisk(ctx, diskName)
		})
		Expect(disk.Name).To(Equal(diskName), "created disk name")

		By("attaching the disk to the node")
		before, err := blockDeviceNames(ctx, nodes, nodeName)
		Expect(err).NotTo(HaveOccurred(), "list node block devices before attach")
		Expect(disks.AttachDisk(ctx, nodeName, diskName)).To(Succeed(),
			"AttachDisk %s to %s", diskName, nodeName)
		Eventually(func(g Gomega) {
			got, err := blockDeviceNames(ctx, nodes, nodeName)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(got).To(HaveLen(len(before)+1),
				"a new block device should appear on the node (before: %v)", before)
		}).WithContext(ctx).WithTimeout(deviceWaitTimeout).WithPolling(devicePollInterval).Should(Succeed())

		By("detaching the disk")
		Expect(disks.DetachDisk(ctx, nodeName, diskName)).To(Succeed(),
			"DetachDisk %s from %s", diskName, nodeName)
		Eventually(func(g Gomega) {
			got, err := blockDeviceNames(ctx, nodes, nodeName)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(got).To(HaveLen(len(before)),
				"the block device should disappear from the node (before: %v)", before)
		}).WithContext(ctx).WithTimeout(deviceWaitTimeout).WithPolling(devicePollInterval).Should(Succeed())

		By("deleting the disk")
		Expect(disks.DeleteDisk(ctx, diskName)).To(Succeed(), "DeleteDisk %s", diskName)
	}, SpecTimeout(45*time.Minute))

	It("converges when attaching an already attached disk", Label("disks"), func(ctx SpecContext) {
		if os.Getenv("E2E_TEST_CLUSTER_PROVIDER") == "" {
			Skip("E2E_TEST_CLUSTER_PROVIDER is not set — no provisioned cluster to attach to")
		}

		cl, nodeName := connectAndPickWorker(ctx, "storage-e2e-disk-attach-idempotent")

		disks := cl.Disks()
		diskName := fmt.Sprintf("e2e-disk-%s", rand.String(5))

		_, err := disks.CreateDisk(ctx, e2esdk.DiskSpec{Name: diskName, Size: resource.MustParse("1Gi")})
		if errors.Is(err, e2esdk.ErrDisksUnsupported) {
			Skip(fmt.Sprintf("provider %q does not support disk management: %v", cl.ProviderName(), err))
		}
		Expect(err).NotTo(HaveOccurred(), "CreateDisk %s", diskName)
		DeferCleanup(func(ctx SpecContext) {
			// Best effort: the disk may already be detached or gone.
			_ = disks.DetachDisk(ctx, nodeName, diskName)
			_ = disks.DeleteDisk(ctx, diskName)
		})

		Expect(disks.AttachDisk(ctx, nodeName, diskName)).To(Succeed(),
			"first AttachDisk of %s to %s", diskName, nodeName)
		Expect(disks.AttachDisk(ctx, nodeName, diskName)).To(Succeed(),
			"second AttachDisk on an already attached disk must converge")
	}, SpecTimeout(45*time.Minute))
})
