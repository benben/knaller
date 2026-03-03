package knaller

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// vmDataDir returns the directory where a VM's data is stored. Each VM gets
// its own directory at ~/.local/share/knaller/vms/<name>/ which holds the
// rootfs copy and any other per-VM files.
func vmDataDir(name string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "knaller", "vms", name)
}

// prepareDisk copies the base rootfs image to a per-VM directory so each VM
// has its own writable filesystem. Uses cp --reflink=auto to get copy-on-write
// behavior on filesystems that support it (btrfs, xfs), which makes the copy
// nearly instant and only uses disk space for blocks that the VM actually changes.
func prepareDisk(name, baseRootFS string) (string, error) {
	dir := vmDataDir(name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create vm dir: %w", err)
	}
	dst := filepath.Join(dir, "rootfs.ext4")
	cmd := exec.Command("cp", "--reflink=auto", baseRootFS, dst)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("copy rootfs: %s: %w", out, err)
	}
	return dst, nil
}

// hostNameservers reads the host's /etc/resolv.conf and returns usable DNS
// server addresses. It skips localhost entries like 127.0.0.53 (systemd-resolved
// stub) since those won't work inside the VM — the guest has its own network
// stack. If only localhost entries are found, it asks systemd-resolved for
// the real upstream servers. As a last resort, falls back to 1.1.1.1 and 8.8.8.8.
func hostNameservers() []string {
	f, err := os.Open("/etc/resolv.conf")
	if err != nil {
		return nil
	}
	defer f.Close()

	var ns []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "nameserver ") {
			addr := strings.TrimSpace(strings.TrimPrefix(line, "nameserver"))
			if addr == "127.0.0.53" || addr == "127.0.0.1" || addr == "::1" {
				continue
			}
			ns = append(ns, addr)
		}
	}

	// If /etc/resolv.conf only had localhost entries (typical with systemd-resolved),
	// try to get the real upstream DNS servers from resolvectl.
	if len(ns) == 0 {
		ns = resolvedUpstreams()
	}

	// Last resort: use well-known public DNS servers.
	if len(ns) == 0 {
		ns = []string{"1.1.1.1", "8.8.8.8"}
	}
	return ns
}

// resolvedUpstreams queries systemd-resolved for the actual upstream DNS servers.
// Many Linux distros configure /etc/resolv.conf to point at 127.0.0.53 (the
// local stub resolver), which is useless inside a VM. The "resolvectl dns"
// command output looks like:
//
//	Link 2 (enp5s0): 10.0.0.1 fd00::1
//
// We parse the IP addresses after "):" and skip IPv6 for simplicity.
func resolvedUpstreams() []string {
	out, err := exec.Command("resolvectl", "dns").Output()
	if err != nil {
		return nil
	}
	var ns []string
	for _, line := range strings.Split(string(out), "\n") {
		if idx := strings.Index(line, "):"); idx >= 0 {
			for _, addr := range strings.Fields(line[idx+2:]) {
				if !strings.Contains(addr, ":") {
					ns = append(ns, addr)
				}
			}
		}
	}
	return ns
}

// removeDisk deletes the per-VM data directory including the rootfs copy.
func removeDisk(name string) error {
	return os.RemoveAll(vmDataDir(name))
}
