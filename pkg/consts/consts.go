/*
Copyright 2026 Rebellions Inc.

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

package consts

const (
	CDIVendor           = "rebellions.ai"
	CDIClass            = "npu"
	CDIKind             = CDIVendor + "/" + CDIClass
	GenericResourceName = CDIKind
	AtomResourceName    = CDIVendor + "/ATOM"
	RebelResourceName   = CDIVendor + "/REBEL"
	BaseCDIDevice       = "runtime"
	RDSCDIClass         = "rds"
	RDSCDIAllDevice     = "all"
	// VFResourceNamePrefix prefixes the resources advertised for SR-IOV
	// virtual functions: rebellions.ai/npu-vf<N>, where N is the parent PF's
	// sriov_numvfs value.
	VFResourceNamePrefix = GenericResourceName + "-vf"
)
