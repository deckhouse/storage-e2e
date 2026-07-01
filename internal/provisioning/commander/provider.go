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

// Package commander implements a clusterprovider.Provider that creates and
// removes test clusters through the Deckhouse Commander API. Bootstrap creates
// a cluster from a template (or reuses one with the same name) and waits for it
// to become Ready; Remove deletes it. Both entry points run as independent
// processes (cmd/bootstrap-cluster, cmd/remove-cluster), so the cluster name is
// taken verbatim from the config rather than randomized.
package commander

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/caarlos0/env/v11"

	internalcluster "github.com/deckhouse/storage-e2e/internal/cluster"
	"github.com/deckhouse/storage-e2e/internal/infrastructure/ssh"
	commanderapi "github.com/deckhouse/storage-e2e/internal/kubernetes/commander"
	"github.com/deckhouse/storage-e2e/pkg/clusterprovider"
)

type commanderProvider struct {
	cfg    *clusterprovider.ClusterConfig
	conf   *Config
	client *commanderapi.Client
	logger *slog.Logger
}

// NewCommanderProvider builds a Commander-backed provider. It parses the
// E2E_COMMANDER_* environment into a Config and constructs the API client.
func NewCommanderProvider(logger *slog.Logger, cfg *clusterprovider.ClusterConfig) (clusterprovider.Provider, error) {
	conf := &Config{}
	if err := env.Parse(conf); err != nil {
		return nil, fmt.Errorf("parse commander config: %w", err)
	}

	authMethod := commanderapi.AuthMethod(conf.AuthMethod)
	if authMethod == "" {
		authMethod = commanderapi.AuthMethodXAuthToken
	}

	client, err := commanderapi.NewClientWithOptions(conf.URL, conf.Token, commanderapi.ClientOptions{
		InsecureSkipTLSVerify: conf.InsecureSkipTLSVerify,
		CACertPath:            conf.CACertPath,
		AuthMethod:            authMethod,
		AuthUser:              conf.AuthUser,
		APIPrefix:             conf.APIPrefix,
	})
	if err != nil {
		return nil, fmt.Errorf("create commander client: %w", err)
	}

	return &commanderProvider{
		cfg:    cfg,
		conf:   conf,
		client: client,
		logger: logger,
	}, nil
}

func (p *commanderProvider) Name() string { return clusterprovider.ModeCommander }

// Bootstrap ensures a Ready cluster named conf.ClusterName exists in Commander,
// creating it from the configured template when absent. It is idempotent: a
// re-run against an already-created (or in-progress) cluster waits for it to
// become Ready instead of failing.
func (p *commanderProvider) Bootstrap(ctx context.Context) error {
	name := p.conf.ClusterName

	p.logger.Info("checking whether cluster exists in Commander",
		"cluster", name, "url", p.conf.URL)

	cluster, err := p.client.GetCluster(ctx, name)
	switch {
	case err == nil:
		p.logger.Info("cluster already exists in Commander",
			"cluster", name, "phase", cluster.Status.Phase)
	case errors.Is(err, commanderapi.ErrClusterNotFound):
		p.logger.Info("cluster not found, creating from template",
			"cluster", name, "template", p.conf.TemplateName)
		cluster, err = p.createCluster(ctx, name)
		if err != nil {
			return fmt.Errorf("create cluster %q: %w", name, err)
		}
		p.logger.Info("cluster creation initiated", "cluster", name)
	default:
		return fmt.Errorf("look up cluster %q in Commander: %w", name, err)
	}

	if cluster.Status.Phase == commanderapi.ClusterPhaseReady {
		p.logger.Info("cluster is already Ready", "cluster", name)
	} else {
		p.logger.Info("waiting for cluster to become Ready",
			"cluster", name, "timeout", p.conf.WaitTimeout)
		if _, err := p.client.WaitForClusterReady(ctx, name, p.conf.WaitTimeout); err != nil {
			return fmt.Errorf("wait for cluster %q to become Ready: %w", name, err)
		}
		p.logger.Info("cluster is Ready", "cluster", name)
	}

	return p.exportKubeconfig(ctx, name)
}

// exportKubeconfig writes the cluster's kubeconfig to conf.KubeconfigOut (when
// set) so downstream pipeline steps can reach the cluster. It first tries the
// Commander API; deployments that do not expose the kubeconfig over the API
// fall back to fetching it over SSH from the master (typically via a jump host),
// which is how storage-e2e's own commander path retrieves it.
func (p *commanderProvider) exportKubeconfig(ctx context.Context, name string) error {
	if p.conf.KubeconfigOut == "" {
		return nil
	}

	if kubeconfig, err := p.client.GetClusterKubeconfig(ctx, name); err == nil && kubeconfig != "" {
		if writeErr := os.WriteFile(p.conf.KubeconfigOut, []byte(kubeconfig), 0o600); writeErr != nil {
			return fmt.Errorf("write kubeconfig to %q: %w", p.conf.KubeconfigOut, writeErr)
		}
		p.logger.Info("wrote cluster kubeconfig (Commander API)", "cluster", name, "path", p.conf.KubeconfigOut)
		return nil
	} else {
		p.logger.Warn("kubeconfig not available via Commander API; falling back to SSH", "cluster", name, "err", err)
	}

	return p.exportKubeconfigViaSSH(ctx, name)
}

// exportKubeconfigViaSSH fetches the kubeconfig from the master over SSH (via a
// jump host when configured) and writes it to conf.KubeconfigOut. The master
// host/user come from the Commander connection info; the SSH key (and jump
// host) come from the provider config.
func (p *commanderProvider) exportKubeconfigViaSSH(ctx context.Context, name string) error {
	if p.conf.SSHPrivateKeyPath == "" {
		return fmt.Errorf("kubeconfig unavailable via Commander API and no SSH key configured (E2E_COMMANDER_SSH_PRIVATE_KEY_PATH); cannot fetch kubeconfig for %q", name)
	}

	conn, err := p.client.GetClusterConnectionInfo(ctx, name)
	if err != nil {
		return fmt.Errorf("get connection info for cluster %q: %w", name, err)
	}
	if conn.SSHHost == "" {
		return fmt.Errorf("Commander returned no SSH host for cluster %q (connection_hosts.masters empty)", name)
	}

	user := p.conf.SSHUser
	if user == "" {
		user = conn.SSHUser
	}
	if user == "" {
		return fmt.Errorf("no SSH user for the master of cluster %q (set E2E_COMMANDER_SSH_USER)", name)
	}

	var sshClient ssh.SSHClient
	if p.conf.SSHJumpHost != "" {
		jumpUser := p.conf.SSHJumpUser
		if jumpUser == "" {
			jumpUser = user
		}
		jumpKey := p.conf.SSHJumpKeyPath
		if jumpKey == "" {
			jumpKey = p.conf.SSHPrivateKeyPath
		}
		p.logger.Info("fetching kubeconfig over SSH via jump host",
			"cluster", name, "jump", fmt.Sprintf("%s@%s", jumpUser, p.conf.SSHJumpHost), "master", fmt.Sprintf("%s@%s", user, conn.SSHHost))
		sshClient, err = ssh.NewClientWithJumpHost(jumpUser, p.conf.SSHJumpHost, jumpKey, user, conn.SSHHost, p.conf.SSHPrivateKeyPath)
	} else {
		p.logger.Info("fetching kubeconfig over SSH", "cluster", name, "master", fmt.Sprintf("%s@%s", user, conn.SSHHost))
		sshClient, err = ssh.NewClient(user, conn.SSHHost, p.conf.SSHPrivateKeyPath)
	}
	if err != nil {
		return fmt.Errorf("create SSH client to master of cluster %q: %w", name, err)
	}
	defer sshClient.Close()

	outDir := filepath.Dir(p.conf.KubeconfigOut)
	_, fetchedPath, err := internalcluster.GetKubeconfig(ctx, conn.SSHHost, user, p.conf.SSHPrivateKeyPath, sshClient, outDir)
	if err != nil {
		return fmt.Errorf("fetch kubeconfig over SSH for cluster %q: %w", name, err)
	}

	data, err := os.ReadFile(fetchedPath)
	if err != nil {
		return fmt.Errorf("read fetched kubeconfig %q: %w", fetchedPath, err)
	}
	if err := os.WriteFile(p.conf.KubeconfigOut, data, 0o600); err != nil {
		return fmt.Errorf("write kubeconfig to %q: %w", p.conf.KubeconfigOut, err)
	}
	p.logger.Info("wrote cluster kubeconfig (SSH)", "cluster", name, "path", p.conf.KubeconfigOut)

	// The fetched kubeconfig points the API server at the node-local proxy
	// (e.g. https://127.0.0.1:6445), so it is only usable through an SSH tunnel
	// to the master. Record the master host/user in a sidecar file so downstream
	// pipeline steps (enable-modules, run-tests) can open that tunnel.
	sshInfo := fmt.Sprintf("host=%s\nuser=%s\n", conn.SSHHost, user)
	sshInfoPath := p.conf.KubeconfigOut + ".sshinfo"
	if err := os.WriteFile(sshInfoPath, []byte(sshInfo), 0o600); err != nil {
		return fmt.Errorf("write kubeconfig ssh-info to %q: %w", sshInfoPath, err)
	}
	p.logger.Info("wrote kubeconfig ssh-info", "path", sshInfoPath, "master", fmt.Sprintf("%s@%s", user, conn.SSHHost))
	return nil
}

// Remove deletes the cluster from Commander. A cluster that is already gone is
// treated as success so teardown is idempotent.
func (p *commanderProvider) Remove(ctx context.Context) error {
	name := p.conf.ClusterName
	p.logger.Info("removing cluster from Commander", "cluster", name)

	if err := p.client.DeleteCluster(ctx, name); err != nil {
		if errors.Is(err, commanderapi.ErrClusterNotFound) {
			p.logger.Info("cluster already absent in Commander, nothing to remove", "cluster", name)
			return nil
		}
		return fmt.Errorf("delete cluster %q: %w", name, err)
	}
	p.logger.Info("cluster deletion initiated", "cluster", name)
	return nil
}

// createCluster resolves the template, its version and (optionally) the
// registry, then issues the create request.
func (p *commanderProvider) createCluster(ctx context.Context, name string) (*commanderapi.Cluster, error) {
	template, err := p.client.GetClusterTemplateByName(ctx, p.conf.TemplateName)
	if err != nil {
		return nil, fmt.Errorf("resolve template %q: %w", p.conf.TemplateName, err)
	}

	versionID, err := resolveTemplateVersionID(template, p.conf.TemplateVersion)
	if err != nil {
		return nil, err
	}
	p.logger.Info("resolved template version", "template", p.conf.TemplateName, "versionID", versionID)

	var registryID string
	if p.conf.RegistryName != "" {
		registry, regErr := p.client.GetRegistryByName(ctx, p.conf.RegistryName)
		if regErr != nil {
			return nil, fmt.Errorf("resolve registry %q: %w", p.conf.RegistryName, regErr)
		}
		registryID = registry.ID
		p.logger.Info("resolved registry", "registry", p.conf.RegistryName, "registryID", registryID)
	}

	values, err := p.buildValues(name)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.CreateClusterFromTemplate(ctx, name, versionID, registryID, values)
	if err != nil {
		return nil, err
	}

	phase := commanderapi.ClusterPhase(resp.Status)
	if phase == "" {
		phase = commanderapi.ClusterPhase(resp.Phase)
	}
	return &commanderapi.Cluster{Status: commanderapi.ClusterStatus{Phase: phase, Message: resp.Message}}, nil
}

// buildValues assembles the template input values: the optional JSON blob from
// E2E_COMMANDER_VALUES with the mandatory "prefix" (== cluster name) on top.
func (p *commanderProvider) buildValues(name string) (map[string]interface{}, error) {
	values := make(map[string]interface{})
	if p.conf.InputValues != "" {
		if err := json.Unmarshal([]byte(p.conf.InputValues), &values); err != nil {
			return nil, fmt.Errorf("parse E2E_COMMANDER_VALUES as JSON: %w", err)
		}
	}
	values["prefix"] = name
	return values, nil
}

// resolveTemplateVersionID picks the template version ID to create from:
// the explicitly requested version (matched by name or ID), else the template's
// current version, else the first available one.
func resolveTemplateVersionID(template *commanderapi.ClusterTemplateResponse, requested string) (string, error) {
	versions := template.ClusterTemplateVersions
	if len(versions) == 0 {
		versions = template.Versions
	}

	if requested != "" {
		for _, v := range versions {
			if v.Name == requested || v.ID == requested {
				return v.ID, nil
			}
		}
		available := make([]string, 0, len(versions))
		for _, v := range versions {
			available = append(available, fmt.Sprintf("%s (%s)", v.Name, v.ID))
		}
		return "", fmt.Errorf("template version %q not found; available: %v", requested, available)
	}

	if template.CurrentClusterTemplateVersionID != "" {
		return template.CurrentClusterTemplateVersionID, nil
	}
	if len(versions) > 0 {
		return versions[0].ID, nil
	}
	return "", fmt.Errorf("template %q has no versions available", template.Name)
}
