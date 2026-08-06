package consts

const (
	CDIVendor           = "rebellions.ai"
	CDIClass            = "npu"
	CDIKind             = CDIVendor + "/" + CDIClass
	GenericResourceName = CDIKind
	AtomResourceName    = CDIVendor + "/ATOM"
	RebelResourceName   = CDIVendor + "/REBEL"
	// VFResourceNamePrefix prefixes the resources advertised for SR-IOV
	// virtual functions: rebellions.ai/npu-vf<N>, where N is the parent PF's
	// sriov_numvfs value.
	VFResourceNamePrefix = GenericResourceName + "-vf"
	BaseCDIDevice        = "runtime"
)
