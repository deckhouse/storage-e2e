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
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/caarlos0/env/v11"
	"k8s.io/client-go/rest"

	"github.com/deckhouse/storage-e2e/internal/config"
	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
	"github.com/deckhouse/storage-e2e/pkg/kubernetes"
)

const vmProvisionPollInterval = 5 * time.Second

type dvpProvider struct {
	cfg     *clusterprovider.ClusterConfig
	dvpConf *Config
	creds   Credentials
	logger  *slog.Logger
	deps    deps
}

func NewDVPProvider(logger *slog.Logger, cfg *clusterprovider.ClusterConfig) (clusterprovider.Provider, error) {
	dvpConf, err := LoadConfig(env.ToMap(os.Environ()))
	if err != nil {
		return nil, err
	}

	creds, err := dvpConf.Resolve()
	if err != nil {
		return nil, fmt.Errorf("resolving credentials: %w", err)
	}

	connector := newConnector(dvpConf, creds, logger)
	d := deps{
		connector:  connector,
		masterConn: connector,
		resolver:   defaultMasterResolver{},
		kube:       defaultKubeOps{},
		fleet:      defaultFleetFactory{dvpConf: dvpConf, logger: logger},
		virt:       defaultVirtFactory{},
	}
	return newProvider(logger, cfg, dvpConf, creds, d), nil
}

func newProvider(logger *slog.Logger, cfg *clusterprovider.ClusterConfig, dvpConf *Config, creds Credentials, d deps) *dvpProvider {
	return &dvpProvider{
		cfg:     cfg,
		dvpConf: dvpConf,
		creds:   creds,
		logger:  logger,
		deps:    d,
	}
}

func (p *dvpProvider) Name() string { return clusterprovider.ModeDVP }

// Connect attaches a test run to the cluster the DVP provider bootstrapped,
// satisfying clusterprovider.Connector. In the split bootstrap/run-tests model
// (see cmd/bootstrap-cluster and .github/workflows/e2e.yml) the suite process
// is separate from the one that ran Bootstrap, so it only has the static
// cluster_config.yml. Connect therefore (1) reads the first master's hostname
// from that config, (2) resolves the running master VM's internal IP off the
// base cluster's virtualization API, then (3) reuses connectToMaster to fetch
// the admin kubeconfig over SSH and open an API tunnel. The returned cleanup
// closes that tunnel; callers MUST invoke it once the run no longer needs the
// connection. With Connect implemented, cluster.connectViaProvider drives the
// suite's connect through the provider (as it already does for commander)
// instead of the legacy TEST_CLUSTER_CREATE_MODE path.
func (p *dvpProvider) Connect(ctx context.Context) (*rest.Config, func(), error) {
	def, err := config.LoadClusterDefinition(p.cfg.ClusterBootstrapConfigPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load cluster bootstrap config: %w", err)
	}
	masterHostname, err := firstMasterHostname(def)
	if err != nil {
		return nil, nil, err
	}

	masterIP, err := p.resolveMasterIPFromBase(ctx, masterHostname)
	if err != nil {
		return nil, nil, err
	}

	p.logger.Info("connecting to bootstrapped master", "hostname", masterHostname, "ip", masterIP)
	return p.deps.masterConn.connectToMaster(ctx, masterIP)
}

// ConnectBase opens a connection to the DVP base cluster (the infrastructure
// cluster hosting the nested cluster's node VMs), satisfying
// clusterprovider.BaseConnector. Suites use it — via
// TestClusterResources.BaseKubeconfig — to manipulate the VirtualDisks that back
// node disks (attach/detach/resize) directly on the base cluster. The returned
// cleanup closes the base SSH tunnel + client.
func (p *dvpProvider) ConnectBase(ctx context.Context) (*rest.Config, func(), error) {
	p.logger.Info("connecting to DVP base cluster for VM/disk operations")
	return p.deps.connector.Connect(ctx)
}

// resolveMasterIPFromBase opens a short-lived base-cluster connection solely to
// resolve the master VM's IP, then releases it (connectToMaster establishes its
// own SSH tunnel to that IP afterwards).
func (p *dvpProvider) resolveMasterIPFromBase(ctx context.Context, masterHostname string) (string, error) {
	baseKube, baseCleanup, err := p.deps.connector.Connect(ctx)
	if err != nil {
		return "", fmt.Errorf("connect to base cluster: %w", err)
	}
	defer baseCleanup()

	masterIP, err := p.deps.resolver.resolveMasterIP(ctx, baseKube, p.dvpConf.Namespace, masterHostname)
	if err != nil {
		return "", fmt.Errorf("resolve master IP: %w", err)
	}
	return masterIP, nil
}

type cleanupStack struct {
	fns []func()
}

func (s *cleanupStack) push(fn func()) {
	if fn != nil {
		s.fns = append(s.fns, fn)
	}
}

func (s *cleanupStack) run() {
	for i := len(s.fns) - 1; i >= 0; i-- {
		s.fns[i]()
	}
	s.fns = nil
}

func (p *dvpProvider) Bootstrap(ctx context.Context) error {
	if err := p.dvpConf.ValidateForBootstrap(); err != nil {
		return fmt.Errorf("bootstrap config validation: %w", err)
	}

	cleanups := cleanupStack{}
	defer cleanups.run()

	clusterDef, err := p.provision(ctx, &cleanups)
	if err != nil {
		return err
	}

	return p.installDeckhouse(ctx, clusterDef, &cleanups)
}

func (p *dvpProvider) provision(ctx context.Context, cleanups *cleanupStack) (*config.ClusterDefinition, error) {
	clusterDef, err := config.LoadClusterDefinition(p.cfg.ClusterBootstrapConfigPath)
	if err != nil {
		return nil, fmt.Errorf("load cluster bootstrap config: %w", err)
	}

	p.logger.Info("loaded cluster bootstrap config",
		"path", p.cfg.ClusterBootstrapConfigPath,
		"masters", len(clusterDef.Masters),
		"workers", len(clusterDef.Workers),
	)

	sshPublicKey, err := publicKeyFromPrivateKey(p.creds.SSHKey, p.dvpConf.SSHPassphrase)
	if err != nil {
		return nil, fmt.Errorf("derive ssh public key: %w", err)
	}

	kube, baseCleanup, err := p.deps.connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	cleanups.push(baseCleanup)

	p.logger.Info("verifying connectivity to DVP base cluster API server")
	if reachErr := p.deps.kube.CheckReachable(ctx, kube); reachErr != nil {
		return nil, reachErr
	}
	p.logger.Info("DVP base cluster API server is reachable")

	p.logger.Info("waiting for virtualization module to become ready",
		"timeout", config.ModuleCheckTimeout,
	)
	if moduleErr := p.deps.kube.WaitModuleReady(ctx, kube, "virtualization", config.ModuleCheckTimeout); moduleErr != nil {
		return nil, moduleErr
	}
	p.logger.Info("virtualization module is ready")

	p.logger.Info("ensuring test namespace exists",
		"namespace", p.dvpConf.Namespace,
		"timeout", config.NamespaceTimeout,
	)
	nsCtx, cancel := context.WithTimeout(ctx, config.NamespaceTimeout)
	nsErr := p.deps.kube.EnsureNamespace(nsCtx, kube, p.dvpConf.Namespace)
	cancel()
	if nsErr != nil {
		return nil, nsErr
	}
	p.logger.Info("test namespace is ready", "namespace", p.dvpConf.Namespace)

	fleet, err := p.deps.fleet.New(ctx, kube, sshPublicKey)
	if err != nil {
		return nil, err
	}

	if clusterDef.Setup == nil {
		clusterDef.Setup = new(config.DefaultSetupVM)
		p.logger.Info("add setup node", "hostname", clusterDef.Setup.Hostname)
	}

	p.logger.Info("provisioning virtual machines",
		"namespace", p.dvpConf.Namespace,
	)
	if err := fleet.Provision(ctx, clusterDef); err != nil {
		return nil, fmt.Errorf("provision virtual machines: %w", err)
	}
	p.logger.Info("virtual machines provisioned", "namespace", p.dvpConf.Namespace)

	setupIP := clusterDef.Setup.IPAddress

	p.logger.Info("waiting for setup node SSH to become ready",
		"ip", setupIP, "timeout", setupNodeConnectTimeout)
	exec, closeExec, err := p.deps.connector.VMExecutor(ctx, setupIP)
	if err != nil {
		return nil, fmt.Errorf("setup node ssh not ready: %w", err)
	}
	p.logger.Info("setup node SSH is ready", "ip", setupIP)

	p.logger.Info("waiting for setup node Docker to become ready",
		"ip", setupIP, "timeout", dockerReadyTimeout)
	dockerErr := waitDockerReady(ctx, exec, dockerReadyPoll, dockerReadyTimeout)
	closeExec()
	if dockerErr != nil {
		return nil, fmt.Errorf("setup node docker not ready: %w", dockerErr)
	}
	p.logger.Info("setup node Docker is ready", "ip", setupIP)

	return clusterDef, nil
}

func (p *dvpProvider) installDeckhouse(ctx context.Context, def *config.ClusterDefinition, cleanups *cleanupStack) error {
	firstMasterIP, err := firstMasterVMIP(def)
	if err != nil {
		return fmt.Errorf("install: %w", err)
	}

	p.logger.Info("ensuring first master is bootstrapped with dhctl", "masterIP", firstMasterIP)
	if bootstrapErr := p.dhctlBootstrap(ctx, def); bootstrapErr != nil {
		return fmt.Errorf("dhctl bootstrap: %w", bootstrapErr)
	}
	p.logger.Info("first master is bootstrapped", "masterIP", firstMasterIP)

	p.logger.Info("connecting to first master", "masterIP", firstMasterIP)
	target, masterCleanup, err := p.deps.masterConn.connectToMaster(ctx, firstMasterIP)
	if err != nil {
		return fmt.Errorf("connect to master %s: %w", firstMasterIP, err)
	}
	cleanups.push(masterCleanup)
	p.logger.Info("connected to first master", "masterIP", firstMasterIP)

	ngCtx, cancel := context.WithTimeout(ctx, config.NodeGroupTimeout)
	ngErr := kubernetes.CreateStaticNodeGroup(ngCtx, target, workerNodeGroupName)
	cancel()
	if ngErr != nil {
		return fmt.Errorf("create worker nodegroup: %w", ngErr)
	}

	p.logger.Info("waiting for bootstrap secrets", "timeout", config.SecretsWaitTimeout)
	if err := waitBootstrapSecrets(ctx, target, config.SecretsWaitTimeout); err != nil {
		return fmt.Errorf("wait bootstrap secrets: %w", err)
	}

	p.logger.Info("waiting for cluster to become healthy", "timeout", config.ClusterHealthTimeout)
	if err := waitClusterHealthy(ctx, target, config.ClusterHealthTimeout); err != nil {
		return fmt.Errorf("cluster health check: %w", err)
	}
	p.logger.Info("first master is healthy")

	p.logger.Info("joining remaining nodes")
	if err := p.joinNodes(ctx, target, def); err != nil {
		return fmt.Errorf("join nodes: %w", err)
	}

	p.logger.Info("waiting for all nodes to become Ready", "timeout", config.NodesReadyTimeout)
	if err := waitNodesReady(ctx, target, def, config.NodesReadyTimeout); err != nil {
		return fmt.Errorf("wait nodes ready: %w", err)
	}
	p.logger.Info("all nodes are Ready")

	p.logger.Info("enabling modules", "count", len(def.DKPParameters.Modules))
	if err := p.enableModules(ctx, target, def); err != nil {
		return fmt.Errorf("enable modules: %w", err)
	}
	p.logger.Info("modules enabled")

	return nil
}

func (p *dvpProvider) Remove(ctx context.Context) error {
	kube, cleanup, err := p.deps.connector.Connect(ctx)
	if err != nil {
		return err
	}
	defer cleanup()

	fleet, err := p.deps.fleet.New(ctx, kube, "")
	if err != nil {
		return err
	}

	p.logger.Info("tearing down virtual machines",
		"namespace", p.dvpConf.Namespace,
	)
	if err := fleet.Teardown(ctx); err != nil {
		return fmt.Errorf("teardown virtual machines: %w", err)
	}
	p.logger.Info("virtual machines torn down", "namespace", p.dvpConf.Namespace)

	p.logger.Info("deleting test namespace",
		"namespace", p.dvpConf.Namespace,
		"timeout", config.NamespaceTimeout,
	)
	nsCtx, cancel := context.WithTimeout(ctx, config.NamespaceTimeout)
	nsErr := p.deps.kube.DeleteNamespace(nsCtx, kube, p.dvpConf.Namespace)
	cancel()
	if nsErr != nil {
		return nsErr
	}
	p.logger.Info("test namespace deleted", "namespace", p.dvpConf.Namespace)

	return nil
}
