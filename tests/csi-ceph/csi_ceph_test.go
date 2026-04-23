/*
Copyright 2025 Flant JSC

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

package csi_ceph

import (
	"context"
	"fmt"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/pkg/cluster"
	k8s "github.com/deckhouse/storage-e2e/pkg/kubernetes"
	"github.com/deckhouse/storage-e2e/pkg/testkit"
)

const (
	// testStorageClassName matches what csi-ceph's smoke test in
	// /csi-ceph/e2e also expects, so the two can share a cluster.
	testStorageClassName = "e2e-ceph-rbd-r1"
	testNamespace        = "e2e-csi-ceph-smoke"
	testPVCName          = "e2e-csi-ceph-smoke-pvc"
)

var _ = Describe("csi-ceph smoke (storage-e2e reference)", Ordered, func() {
	var testClusterResources *cluster.TestClusterResources

	BeforeAll(func() {
		cluster.OutputEnvironmentVariables()
	})

	AfterAll(func() {
		cluster.CleanupTestClusterResources(testClusterResources)
	})

	It("should create or connect to test cluster", func() {
		testClusterResources = cluster.CreateOrConnectToTestCluster()
		Expect(testClusterResources).NotTo(BeNil())
		Expect(testClusterResources.Kubeconfig).NotTo(BeNil())
	})

	It("should ensure Ceph RBD StorageClass via Rook (EnsureCephStorageClass)", func() {
		Expect(testClusterResources).NotTo(BeNil())

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
		defer cancel()

		cfg := testkit.CephStorageClassConfig{
			StorageClassName: testStorageClassName,
			ReplicaSize:      1,
			FailureDomain:    "osd",

			// When OSDStorageClass is empty EnsureCephStorageClass will fall
			// back to EnsureDefaultStorageClass to create a sds-local-volume
			// Thick SC on the fly.
			OSDStorageClass:          os.Getenv("CSI_CEPH_OSD_STORAGE_CLASS"),
			OSDBackingIncludeMasters: true,

			// Let callers pin a specific csi-ceph image from a dev-registry PR.
			CsiCephModulePullOverride: os.Getenv("CSI_CEPH_MODULE_PULL_OVERRIDE"),
		}

		// VirtualDisk attachment for nested-VM clusters.
		if testClusterResources.VMResources != nil {
			cfg.OSDBackingBaseKubeconfig = testClusterResources.BaseKubeconfig
			cfg.OSDBackingVMNamespace = testClusterResources.VMResources.Namespace
			cfg.OSDBackingBaseStorageClassName = config.TestClusterStorageClass
		}

		scName, err := testkit.EnsureCephStorageClass(ctx, testClusterResources.Kubeconfig, cfg)
		Expect(err).NotTo(HaveOccurred(), "EnsureCephStorageClass")
		Expect(scName).To(Equal(testStorageClassName))
	})

	It("should provision a PVC against the Ceph StorageClass", func() {
		Expect(testClusterResources).NotTo(BeNil())

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		_, err := k8s.CreateNamespaceIfNotExists(ctx, testClusterResources.Kubeconfig, testNamespace)
		Expect(err).NotTo(HaveOccurred(), "create test namespace")

		apply, err := k8s.NewApplyClient(testClusterResources.Kubeconfig)
		Expect(err).NotTo(HaveOccurred(), "create apply client")

		pvcYAML := fmt.Sprintf(`apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %s
  namespace: %s
  labels:
    e2e.csi-ceph/smoke: "true"
spec:
  accessModes: [ "ReadWriteOnce" ]
  resources:
    requests:
      storage: 1Gi
  storageClassName: %s
`, testPVCName, testNamespace, testStorageClassName)

		Expect(apply.ApplyYAML(ctx, pvcYAML, testNamespace)).To(Succeed(), "apply PVC")

		clientset, err := k8s.NewClientsetWithRetry(ctx, testClusterResources.Kubeconfig)
		Expect(err).NotTo(HaveOccurred(), "clientset")

		Expect(k8s.WaitForPVCsBound(ctx, clientset, testNamespace, "e2e.csi-ceph/smoke=true", 1, 60, 5*time.Second)).
			To(Succeed(), "wait PVC bound")
	})
})
