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
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"k8s.io/client-go/rest"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/storage"
	"github.com/deckhouse/storage-e2e/internal/kubernetes/virtualization"
	"github.com/deckhouse/storage-e2e/internal/logger"
)

// BlockDeviceSerialFromUID returns the hex(MD5(uid)) serial used by the sds-node-configurator agent.
func BlockDeviceSerialFromUID(uid string) string {
	h := md5.Sum([]byte(uid))
	return hex.EncodeToString(h[:])
}

// WaitConsumableBlockDeviceForVirtualDisk polls until a consumable BlockDevice appears for the given VirtualDisk attachment on targetVM.
func WaitConsumableBlockDeviceForVirtualDisk(
	ctx context.Context,
	nestedKube, baseKube *rest.Config,
	namespace, diskName, attachmentName, targetVM string,
	timeout time.Duration,
) (*snc.BlockDevice, error) {
	virtClient, err := virtualization.NewClient(ctx, baseKube)
	if err != nil {
		return nil, fmt.Errorf("virtualization client: %w", err)
	}
	vdObj, err := virtClient.VirtualDisks().Get(ctx, namespace, diskName)
	if err != nil {
		return nil, fmt.Errorf("get VirtualDisk %s: %w", diskName, err)
	}
	attObj, err := virtClient.VirtualMachineBlockDeviceAttachments().Get(ctx, namespace, attachmentName)
	if err != nil {
		return nil, fmt.Errorf("get VMBDA %s: %w", attachmentName, err)
	}
	serialVD := BlockDeviceSerialFromUID(string(vdObj.GetUID()))
	serialAtt := BlockDeviceSerialFromUID(string(attObj.GetUID()))

	bdClient, err := storage.NewBlockDeviceClient(ctx, nestedKube)
	if err != nil {
		return nil, err
	}

	deadline := time.Now().Add(timeout)
	poll := 10 * time.Second
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting for consumable BlockDevice for disk %s on VM %s", diskName, targetVM)
		}

		list, err := bdClient.List(ctx)
		if err != nil {
			logger.Warn("list BlockDevices: %v", err)
			time.Sleep(poll)
			continue
		}
		for i := range list.Items {
			bd := &list.Items[i]
			s := strings.TrimSpace(bd.Status.Serial)
			if s != serialVD && s != serialAtt {
				continue
			}
			if bd.Status.NodeName != targetVM {
				continue
			}
			if !bd.Status.Consumable || bd.Status.Size.IsZero() || bd.Status.Path == "" || !strings.HasPrefix(bd.Status.Path, "/dev/") {
				continue
			}
			return bd, nil
		}
		time.Sleep(poll)
	}
}
