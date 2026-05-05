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

package kubernetes

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// DefaultDebugImage is the image ReadFileFromDistrolessPod injects as the
// short-lived ephemeral container. busybox ships cat, sleep and a
// minimal sh — exactly the toolset we need to read /proc/1/root/<path>
// in the target container's filesystem. Tests against an air-gapped
// registry can override this via ReadFileOptions.DebugImage.
const DefaultDebugImage = "busybox:1.36"

// DefaultEphemeralStartupTimeout caps the wait for the injected
// ephemeral container to transition into Running. Image pull from a
// warm registry usually takes a couple of seconds; 60 s is a generous
// upper bound that still surfaces ImagePullBackOff/ErrImagePull early.
const DefaultEphemeralStartupTimeout = 60 * time.Second

// ephemeralPollInterval is how often we re-Get the pod when waiting for
// the ephemeral container to start. 500 ms is a deliberate compromise:
// fast enough that the typical 1-3 s pull is observed promptly, slow
// enough that we don't hammer the apiserver.
const ephemeralPollInterval = 500 * time.Millisecond

// ReadFileOptions tunes ReadFileFromDistrolessPod.
type ReadFileOptions struct {
	// DebugImage overrides the ephemeral container image. Defaults to
	// DefaultDebugImage. Use this on air-gapped clusters to point at an
	// internal mirror.
	DebugImage string
	// StartupTimeout caps the wait for the ephemeral container to reach
	// state.Running. Defaults to DefaultEphemeralStartupTimeout.
	StartupTimeout time.Duration
}

// ExecInPod runs cmd inside container of pod namespace/pod via the
// apiserver's pods/exec subresource and returns stdout and stderr
// separately, plus any transport- or exec-level error.
//
// The container must ship every binary referenced by cmd; ExecInPod does
// NOT inject any helper. For distroless containers without cat / sh,
// see ReadFileFromDistrolessPod.
func ExecInPod(
	ctx context.Context,
	kubeconfig *rest.Config,
	namespace, pod, container string,
	cmd []string,
) (stdout, stderr string, err error) {
	clientset, err := NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return "", "", fmt.Errorf("create clientset: %w", err)
	}

	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   cmd,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(kubeconfig, "POST", req.URL())
	if err != nil {
		return "", "", fmt.Errorf("create SPDY executor for %s/%s[%s]: %w",
			namespace, pod, container, err)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdoutBuf,
		Stderr: &stderrBuf,
	})
	stdout = stdoutBuf.String()
	stderr = stderrBuf.String()
	if err != nil {
		return stdout, stderr, fmt.Errorf("exec %v in %s/%s[%s]: %w (stderr=%q)",
			cmd, namespace, pod, container, err, stderr)
	}
	return stdout, stderr, nil
}

// ReadFileFromPod cat's `path` from inside `container` of pod
// `namespace/pod`. Equivalent to `kubectl exec -c container -- cat
// path`, with stderr surfaced as part of the error if non-empty.
//
// Requires the container image to ship cat. For distroless / scratch
// images, use ReadFileFromDistrolessPod.
func ReadFileFromPod(
	ctx context.Context,
	kubeconfig *rest.Config,
	namespace, pod, container, path string,
) (string, error) {
	stdout, stderr, err := ExecInPod(ctx, kubeconfig, namespace, pod, container, []string{"cat", path})
	if err != nil {
		return stdout, err
	}
	if stderr != "" {
		return stdout, fmt.Errorf("cat %s in %s/%s[%s] reported stderr: %s",
			path, namespace, pod, container, stderr)
	}
	return stdout, nil
}

// ReadFileFromDistrolessPod reads `path` from inside `targetContainer`
// of pod `namespace/pod` even when targetContainer ships no shell, no
// cat and no tar — i.e. a distroless or scratch image like
// csi-controller. It does so by injecting a short-lived ephemeral
// container (TargetContainerName=targetContainer, which gives it a
// shared PID namespace with the target) and then catting
// /proc/1/root<path>. /proc/1 is PID 1 inside the target container's
// PID namespace, and /proc/<pid>/root is the well-known kernel-exposed
// view of that process's filesystem root.
//
// Why this does NOT restart the target pod or any of its containers:
//
//   - Ephemeral containers are added through the dedicated
//     /pods/<name>/ephemeralcontainers subresource (UpdateEphemeralContainers
//     in client-go). The apiserver explicitly allows this mutation on a
//     running pod; the ordinary pod PUT/PATCH path that would trigger
//     re-creation is bypassed entirely. Without this dedicated path,
//     adding a container to a live pod would be flat-out forbidden.
//   - metadata.generation, spec.containers, the pod sandbox UID and the
//     ReplicaSet/DaemonSet observation all stay intact. The kubelet
//     simply launches the new container in the existing pod sandbox
//     without disturbing existing containers. Workload-controller
//     rollouts and pod-template `checksum/...` annotations are not
//     affected, so e2e suites that subsequently assert on rollout
//     state see a clean signal — the FS read does not contaminate it.
//   - Ephemeral containers are forbidden from declaring ports, probes,
//     lifecycle hooks or resources, which guarantees the inject is a
//     cheap no-op for the pod's lifecycle.
//
// Caveat: ephemeral containers cannot be removed once added. The cat
// process exits with the container after `sleep 60`, but the entry
// remains in pod.spec.ephemeralContainers and
// pod.status.ephemeralContainerStatuses (state=Terminated). For
// long-running suites those entries simply pile up until the next pod
// recycle. Each invocation here generates a unique container name, so
// repeat calls against the same pod are safe.
func ReadFileFromDistrolessPod(
	ctx context.Context,
	kubeconfig *rest.Config,
	namespace, pod, targetContainer, path string,
	opts ReadFileOptions,
) (string, error) {
	if opts.DebugImage == "" {
		opts.DebugImage = DefaultDebugImage
	}
	if opts.StartupTimeout <= 0 {
		opts.StartupTimeout = DefaultEphemeralStartupTimeout
	}

	clientset, err := NewClientsetWithRetry(ctx, kubeconfig)
	if err != nil {
		return "", fmt.Errorf("create clientset: %w", err)
	}
	pods := clientset.CoreV1().Pods(namespace)

	ecName, err := randomEphemeralName("filereader-")
	if err != nil {
		return "", fmt.Errorf("generate ephemeral container name: %w", err)
	}

	livePod, err := pods.Get(ctx, pod, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get pod %s/%s: %w", namespace, pod, err)
	}
	livePod.Spec.EphemeralContainers = append(livePod.Spec.EphemeralContainers, corev1.EphemeralContainer{
		EphemeralContainerCommon: corev1.EphemeralContainerCommon{
			Name:                     ecName,
			Image:                    opts.DebugImage,
			Command:                  []string{"sleep", "60"},
			ImagePullPolicy:          corev1.PullIfNotPresent,
			TerminationMessagePolicy: corev1.TerminationMessageReadFile,
		},
		TargetContainerName: targetContainer,
	})
	if _, err := pods.UpdateEphemeralContainers(ctx, pod, livePod, metav1.UpdateOptions{}); err != nil {
		return "", fmt.Errorf("inject ephemeral container %q into %s/%s: %w",
			ecName, namespace, pod, err)
	}

	if err := waitEphemeralContainerRunning(ctx, pods, pod, ecName, opts.StartupTimeout); err != nil {
		return "", err
	}

	stdout, stderr, err := ExecInPod(ctx, kubeconfig, namespace, pod, ecName, []string{"cat", "/proc/1/root" + path})
	if err != nil {
		return stdout, fmt.Errorf("read %s from %s/%s[%s] via ephemeral %s: %w",
			path, namespace, pod, targetContainer, ecName, err)
	}
	if stderr != "" {
		return stdout, fmt.Errorf("read %s from %s/%s[%s] via ephemeral %s: stderr=%s",
			path, namespace, pod, targetContainer, ecName, stderr)
	}
	return stdout, nil
}

// waitEphemeralContainerRunning polls pod.status.ephemeralContainerStatuses
// until the container with name ecName reports state.Running != nil.
// Returns immediately on Terminated / hard pull failures so tests don't
// have to sit through the full timeout when the debug image is
// unreachable.
func waitEphemeralContainerRunning(
	ctx context.Context,
	pods typedcorev1.PodInterface,
	podName, ecName string,
	timeout time.Duration,
) error {
	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(ephemeralPollInterval)
	defer ticker.Stop()

	for {
		p, getErr := pods.Get(deadlineCtx, podName, metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(getErr):
			return fmt.Errorf("pod %s disappeared while waiting for ephemeral container %q",
				podName, ecName)
		case getErr == nil:
			for _, st := range p.Status.EphemeralContainerStatuses {
				if st.Name != ecName {
					continue
				}
				if st.State.Running != nil {
					return nil
				}
				if st.State.Terminated != nil {
					return fmt.Errorf("ephemeral container %q in pod %s terminated before exec: reason=%s exitCode=%d",
						ecName, podName,
						st.State.Terminated.Reason, st.State.Terminated.ExitCode)
				}
				if w := st.State.Waiting; w != nil && (w.Reason == "ImagePullBackOff" || w.Reason == "ErrImagePull") {
					return fmt.Errorf("ephemeral container %q in pod %s cannot start: %s: %s",
						ecName, podName, w.Reason, w.Message)
				}
			}
		}

		select {
		case <-deadlineCtx.Done():
			return fmt.Errorf("timeout (%s) waiting for ephemeral container %q in pod %s to be Running",
				timeout, ecName, podName)
		case <-ticker.C:
		}
	}
}

// randomEphemeralName returns prefix + 8 hex chars from crypto/rand.
// Sufficient entropy for uniqueness across a single test run; we don't
// need cryptographic strength but crypto/rand keeps us out of math/rand
// seeding pitfalls.
func randomEphemeralName(prefix string) (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b[:]), nil
}
