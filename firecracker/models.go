// Package firecracker provides a low-level Go client for the Firecracker VMM API.
//
// Firecracker exposes an HTTP API over a Unix domain socket. This package wraps
// that API with typed Go structs and methods, handling JSON serialization and
// socket communication. It is used internally by the high-level knaller package,
// but can also be used directly for fine-grained control over VM configuration.
package firecracker

// BootSource tells Firecracker where to find the guest kernel and (optionally)
// an initrd. The BootArgs field is passed directly to the kernel as its command
// line. For a serial console, you typically want at least "console=ttyS0". For
// guest networking via kernel command-line IP config, you also include an ip=
// argument here (see networkConfig.bootArgsIP in the knaller package).
type BootSource struct {
	KernelImagePath string `json:"kernel_image_path"`
	BootArgs        string `json:"boot_args,omitempty"`
	InitrdPath      string `json:"initrd_path,omitempty"`
}

// Drive attaches a block device (disk image) to the VM. Firecracker supports
// multiple drives; the root device (IsRootDevice=true) is used as the guest's
// root filesystem. Set IsReadOnly=false if the guest needs to write to the disk.
// RateLimiter is an optional token-bucket rate limiter for disk I/O.
type Drive struct {
	DriveID      string       `json:"drive_id"`
	PathOnHost   string       `json:"path_on_host"`
	IsRootDevice bool         `json:"is_root_device"`
	IsReadOnly   bool         `json:"is_read_only"`
	RateLimiter  *RateLimiter `json:"rate_limiter,omitempty"`
}

// MachineConfig sets the virtual hardware: number of vCPUs and memory size.
// Smt (simultaneous multi-threading) is usually set to false for microVMs.
type MachineConfig struct {
	VcpuCount  int  `json:"vcpu_count"`
	MemSizeMib int  `json:"mem_size_mib"`
	Smt        bool `json:"smt"`
}

// NetworkInterface attaches a TAP device on the host to a virtual NIC in the
// guest. IfaceID is an arbitrary name used to identify the interface in the API.
// HostDevName must be the name of an existing TAP device (e.g. "kn-myvm").
// GuestMAC is optional; Firecracker generates one if omitted.
// RxRateLimiter and TxRateLimiter are optional token-bucket rate limiters
// for inbound and outbound traffic respectively.
type NetworkInterface struct {
	IfaceID        string       `json:"iface_id"`
	HostDevName    string       `json:"host_dev_name"`
	GuestMAC       string       `json:"guest_mac,omitempty"`
	RxRateLimiter  *RateLimiter `json:"rx_rate_limiter,omitempty"`
	TxRateLimiter  *RateLimiter `json:"tx_rate_limiter,omitempty"`
}

// RateLimiter is a Firecracker token-bucket rate limiter. Bandwidth limits the
// sustained throughput (bytes per refill period). Ops limits the number of I/O
// operations per refill period. Either or both may be set.
type RateLimiter struct {
	Bandwidth *TokenBucket `json:"bandwidth,omitempty"`
	Ops       *TokenBucket `json:"ops,omitempty"`
}

// TokenBucket defines a token bucket for rate limiting. Size is the number of
// tokens (bytes for bandwidth, ops for operations) added every RefillTimeMs
// milliseconds. OneTimeBurst is an optional initial burst of extra tokens.
type TokenBucket struct {
	Size         int64 `json:"size"`
	OneTimeBurst int64 `json:"one_time_burst,omitempty"`
	RefillTimeMs int64 `json:"refill_time"`
}

// SnapshotCreate is the request body for PUT /snapshot/create. The VM must be
// paused before creating a snapshot. SnapshotType is "Full" for a complete
// snapshot. SnapshotPath and MemFilePath are where Firecracker writes the
// device state and memory dump respectively.
type SnapshotCreate struct {
	SnapshotType string `json:"snapshot_type"`
	SnapshotPath string `json:"snapshot_path"`
	MemFilePath  string `json:"mem_file_path"`
}

// Action is a command sent to a running Firecracker instance. The two main
// actions are "InstanceStart" (boot the VM) and "SendCtrlAltDel" (graceful
// shutdown — the guest OS handles this like pressing Ctrl+Alt+Del).
type Action struct {
	ActionType string `json:"action_type"`
}

// InstanceInfo is returned by GET / and contains metadata about the running
// Firecracker process, including its state ("Not started", "Running") and
// the VMM version string.
type InstanceInfo struct {
	AppName    string `json:"app_name"`
	ID         string `json:"id"`
	State      string `json:"state"`
	VmmVersion string `json:"vmm_version"`
}

// ErrorResponse is returned by Firecracker when an API call fails.
// The FaultMessage field contains a human-readable error description.
type ErrorResponse struct {
	FaultMessage string `json:"fault_message"`
}

// VMConfig is the full VM configuration returned by GET /vm/config. It includes
// the boot source, all attached drives, machine hardware config, and network
// interfaces. This is useful for discovering the full configuration of a running
// VM without needing to store any state ourselves.
type VMConfig struct {
	BootSource        *BootSource        `json:"boot-source,omitempty"`
	Drives            []Drive            `json:"drives,omitempty"`
	MachineConfig     *MachineConfig     `json:"machine-config,omitempty"`
	NetworkInterfaces []NetworkInterface `json:"network-interfaces,omitempty"`
}
