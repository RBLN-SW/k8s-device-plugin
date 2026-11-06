package consts

const (
	CDIVendor           = "rebellions.ai"
	CDIClass            = "npu"
	CDIKind             = CDIVendor + "/" + CDIClass
	GenericResourceName = CDIKind
	BaseCDIDevice       = "runtime"
	RDSCDIClass         = "rds"
	RDSCDIAllDevice     = "all"
	// VFResourceNamePrefix prefixes the resources advertised for SR-IOV
	// virtual functions: rebellions.ai/npu-vf<N>, where N is the parent PF's
	// sriov_numvfs value.
	VFResourceNamePrefix = GenericResourceName + "-vf"
)
