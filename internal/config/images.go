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
}

