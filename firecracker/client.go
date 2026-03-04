package firecracker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
)

// Client talks to a running Firecracker instance through its Unix socket API.
// Firecracker's API is HTTP-based but served over a Unix domain socket instead
// of TCP. We use Go's standard http.Client with a custom dialer that connects
// to the socket file.
type Client struct {
	http       *http.Client
	socketPath string
}

// NewClient creates a Firecracker API client for the given Unix socket path.
// The socket is created by the Firecracker process when it starts (via the
// --api-sock flag). The client does not verify the socket exists — errors
// will occur when you make API calls if the socket is missing or stale.
func NewClient(socketPath string) *Client {
	return &Client{
		socketPath: socketPath,
		http: &http.Client{
			Transport: &http.Transport{
				// Override the dialer to connect to the Unix socket instead of
				// TCP. The "localhost" in request URLs is just a placeholder —
				// all traffic goes through the socket file.
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", socketPath)
				},
			},
		},
	}
}

// put sends a PUT request with a JSON body. Most Firecracker config endpoints
// use PUT and return 204 No Content on success. If the response contains an
// error, we try to parse it as a Firecracker ErrorResponse for a useful message.
func (c *Client) put(ctx context.Context, path string, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://localhost"+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	respBody, _ := io.ReadAll(resp.Body)
	var errResp ErrorResponse
	if json.Unmarshal(respBody, &errResp) == nil && errResp.FaultMessage != "" {
		return fmt.Errorf("firecracker: %s", errResp.FaultMessage)
	}
	return fmt.Errorf("firecracker: unexpected status %d: %s", resp.StatusCode, respBody)
}

// patch sends a PATCH request with a JSON body. Firecracker uses PATCH for
// the /vm endpoint (pause/resume). Like put, expects 204 No Content on success.
func (c *Client) patch(ctx context.Context, path string, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, "http://localhost"+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	respBody, _ := io.ReadAll(resp.Body)
	var errResp ErrorResponse
	if json.Unmarshal(respBody, &errResp) == nil && errResp.FaultMessage != "" {
		return fmt.Errorf("firecracker: %s", errResp.FaultMessage)
	}
	return fmt.Errorf("firecracker: unexpected status %d: %s", resp.StatusCode, respBody)
}

// get sends a GET request and decodes the JSON response into result.
func (c *Client) get(ctx context.Context, path string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost"+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		var errResp ErrorResponse
		if json.Unmarshal(respBody, &errResp) == nil && errResp.FaultMessage != "" {
			return fmt.Errorf("firecracker: %s", errResp.FaultMessage)
		}
		return fmt.Errorf("firecracker: unexpected status %d: %s", resp.StatusCode, respBody)
	}
	return json.NewDecoder(resp.Body).Decode(result)
}

// SetBootSource configures where Firecracker should find the guest kernel.
// Must be called before StartInstance.
func (c *Client) SetBootSource(ctx context.Context, bs *BootSource) error {
	return c.put(ctx, "/boot-source", bs)
}

// SetDrive attaches a block device (disk image) to the VM.
// Must be called before StartInstance.
func (c *Client) SetDrive(ctx context.Context, d *Drive) error {
	return c.put(ctx, "/drives/"+d.DriveID, d)
}

// SetMachineConfig sets the number of vCPUs and memory for the VM.
// Must be called before StartInstance.
func (c *Client) SetMachineConfig(ctx context.Context, mc *MachineConfig) error {
	return c.put(ctx, "/machine-config", mc)
}

// SetNetworkInterface attaches a host TAP device to a virtual NIC inside the
// guest. Must be called before StartInstance.
func (c *Client) SetNetworkInterface(ctx context.Context, ni *NetworkInterface) error {
	return c.put(ctx, "/network-interfaces/"+ni.IfaceID, ni)
}

// StartInstance boots the VM. All configuration (boot source, drives, network,
// machine config) must be set before calling this. Once started, the VM cannot
// be reconfigured.
func (c *Client) StartInstance(ctx context.Context) error {
	return c.put(ctx, "/actions", &Action{ActionType: "InstanceStart"})
}

// StopInstance sends Ctrl+Alt+Del to the guest OS, triggering a graceful
// shutdown. The guest must handle this signal (most Linux distros do by
// default). After this, wait for the Firecracker process to exit.
func (c *Client) StopInstance(ctx context.Context) error {
	return c.put(ctx, "/actions", &Action{ActionType: "SendCtrlAltDel"})
}

// GetInfo returns metadata about the Firecracker instance (state, version, etc.).
func (c *Client) GetInfo(ctx context.Context) (*InstanceInfo, error) {
	var info InstanceInfo
	if err := c.get(ctx, "/", &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// GetMachineConfig returns the current vCPU and memory configuration.
func (c *Client) GetMachineConfig(ctx context.Context) (*MachineConfig, error) {
	var mc MachineConfig
	if err := c.get(ctx, "/machine-config", &mc); err != nil {
		return nil, err
	}
	return &mc, nil
}

// GetVMConfig returns the complete VM configuration including boot source,
// drives, network interfaces, and machine config. This is useful for discovering
// the full state of a running VM without keeping any local state files.
func (c *Client) GetVMConfig(ctx context.Context) (*VMConfig, error) {
	var vc VMConfig
	if err := c.get(ctx, "/vm/config", &vc); err != nil {
		return nil, err
	}
	return &vc, nil
}

// PauseVM pauses a running VM by freezing its vCPUs. Must be called before
// creating a snapshot.
func (c *Client) PauseVM(ctx context.Context) error {
	return c.patch(ctx, "/vm", &struct {
		State string `json:"state"`
	}{State: "Paused"})
}

// ResumeVM resumes a paused VM, unfreezing its vCPUs.
func (c *Client) ResumeVM(ctx context.Context) error {
	return c.patch(ctx, "/vm", &struct {
		State string `json:"state"`
	}{State: "Resumed"})
}

// CreateSnapshot creates a full snapshot of a paused VM. The VM must be paused
// first via PauseVM. Firecracker writes device state to SnapshotPath and a
// full memory dump to MemFilePath.
func (c *Client) CreateSnapshot(ctx context.Context, sc *SnapshotCreate) error {
	return c.put(ctx, "/snapshot/create", sc)
}

// LoadSnapshot loads a previously saved snapshot into an unconfigured Firecracker
// instance. After loading, the VM is paused and must be resumed via ResumeVM.
// Drive paths can be updated with PatchDrive before resuming.
func (c *Client) LoadSnapshot(ctx context.Context, snapshotPath, memPath string) error {
	return c.put(ctx, "/snapshot/load", map[string]any{
		"snapshot_path": snapshotPath,
		"mem_backend": map[string]any{
			"backend_type": "File",
			"backend_path": memPath,
		},
		"resume_vm": false,
	})
}

// PatchDrive updates the backing file path for a drive. Used after loading a
// snapshot to point the drive at a new rootfs location before resuming.
func (c *Client) PatchDrive(ctx context.Context, driveID, pathOnHost string) error {
	return c.patch(ctx, "/drives/"+driveID, map[string]any{
		"drive_id":     driveID,
		"path_on_host": pathOnHost,
	})
}
