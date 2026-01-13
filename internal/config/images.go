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

package config

// OSTypeMap maps OS type names to their definitions
var OSTypeMap = map[string]OSType{
	"Ubuntu 22.04 6.2.0-39-generic": {
		ImageURL:      "https://cloud-images.ubuntu.com/jammy/current/jammy-server-cloudimg-amd64.img",
		KernelVersion: "6.2.0-39-generic",
	},
	"Ubuntu 24.04 6.8.0-53-generic": {
		ImageURL:      "https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img",
		KernelVersion: "6.8.0-53-generic",
	},
	"RedOS 8.0 6.6.26-1.red80.x86_64": {
		ImageURL:      "https://89d64382-20df-4581-8cc7-80df331f67fa.selstorage.ru/redos/redos-8-1.x86_64.qcow2",
		KernelVersion: "6.6.26-1.red80.x86_64",
	},
	"RedOS 7.3.6 5.15.78-2.el7.3.x86_64": {
		ImageURL:      "https://89d64382-20df-4581-8cc7-80df331f67fa.selstorage.ru/redos/RO732_MIN-STD.qcow2",
		KernelVersion: "5.15.78-2.el7.3.x86_64",
	},
	"Debian 12": {
		ImageURL:      "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.qcow2",
		KernelVersion: "6.1.0-41-cloud-amd64",
	},
	"Debian 13": {
		ImageURL:      "https://cdimage.debian.org/images/cloud/trixie/latest/debian-13-generic-amd64.qcow2",
		KernelVersion: "6.12.57+deb13-amd64",
	},
	"AltLinux Server 10.4": {
		ImageURL:      "https://89d64382-20df-4581-8cc7-80df331f67fa.selstorage.ru/flant-cloud/altlinux/altlinux-10-4-p10.qcow2", // ATTENTION! Official images from AltLinux's site are not downloaded due to bot-protection. Download manually and upload to Selectel!
		KernelVersion: "6.1.130-un-def-alt1",
	},
	"AltLinux Server 11": {
		ImageURL:      "https://89d64382-20df-4581-8cc7-80df331f67fa.selstorage.ru/flant-cloud/altlinux/altlinux-11-0-p11.qcow2", // ATTENTION! Official images from AltLinux's site are not downloaded due to bot-protection. Download manually and upload to Selectel!
		KernelVersion: "6.12.34-6.12-alt1",
	},
	"Astra 1.8.3": {
		ImageURL:      "https://89d64382-20df-4581-8cc7-80df331f67fa.selstorage.ru/flant-cloud/astra/astra-1-8-3-adv-20251016.qcow2", // TODO: check is МКЦ is disabled here!
		KernelVersion: "6.1",
	},
}
