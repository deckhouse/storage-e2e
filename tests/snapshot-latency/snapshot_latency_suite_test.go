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

package snapshot_latency

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/internal/logger"
)

var _ = BeforeSuite(func() {
	Expect(config.ValidateEnvironment()).NotTo(HaveOccurred(), "Failed to validate environment")
	Expect(logger.Initialize()).NotTo(HaveOccurred(), "Failed to initialize logger")
})

var _ = AfterSuite(func() {
	if err := logger.Close(); err != nil {
		GinkgoWriter.Printf("Warning: Failed to close logger: %v\n", err)
	}
})

func TestSnapshotLatency(t *testing.T) {
	RegisterFailHandler(Fail)
	suiteConfig, reporterConfig := GinkgoConfiguration()
	suiteConfig.Timeout = 45 * time.Minute
	reporterConfig.Verbose = true
	reporterConfig.ShowNodeEvents = false
	RunSpecs(t, "Snapshot Latency Suite", suiteConfig, reporterConfig)
}
