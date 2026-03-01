# knaller

knaller ([/ˈknalɐ/](https://de.wiktionary.org/wiki/Knaller)) — a Go library and CLI for running [Firecracker](https://github.com/firecracker-microvm/firecracker) microVMs with a container-like experience.

## Requirements

knaller needs a Linux host with KVM support. Firecracker only runs on Linux (x86_64 and aarch64).

### System requirements

- **Linux with KVM** — `/dev/kvm` must be accessible
- **Root access** — needed for creating TAP network devices and mounting rootfs images
- **Firecracker binary** — download from [Firecracker releases](https://github.com/firecracker-microvm/firecracker/releases)
- **tun kernel module** — usually loaded by default; if not: `modprobe tun`
- **SSH server in guest** — the rootfs image must have an SSH server running (e.g. openssh-server)
- **SSH keypair** — knaller injects your public key (`~/.ssh/id_ed25519.pub`, `id_rsa.pub`, or `id_ecdsa.pub`) into the guest for passwordless `ssh root@<guest-ip>`. Generate one if you don't have one: `ssh-keygen -t ed25519`

### Host networking setup

For VMs to reach the internet, the host must forward packets and NAT the VM traffic. This only needs to be done once:

```bash
# Enable IP forwarding so the host routes packets between the VM and the internet.
sudo sysctl -w net.ipv4.ip_forward=1

# NAT traffic from VMs (172.16.0.0/12) to the internet.
# Replace "eth0" with your actual internet-facing interface (check with: ip route | grep default).
sudo iptables -t nat -A POSTROUTING -o eth0 -s 172.16.0.0/12 -j MASQUERADE

# Allow forwarded traffic to/from VM TAP devices (named kn-*).
sudo iptables -A FORWARD -i kn-+ -j ACCEPT
sudo iptables -A FORWARD -o kn-+ -m state --state RELATED,ESTABLISHED -j ACCEPT
```

### Guest images

Download a Linux kernel and root filesystem for Firecracker:

```bash
mkdir -p ~/.local/share/knaller

# Download kernel
curl -fsSL -o ~/.local/share/knaller/vmlinux https://s3.amazonaws.com/spec.ccfc.min/img/quickstart_guide/x86_64/kernels/vmlinux.bin

# Download rootfs
curl -fsSL -o ~/.local/share/knaller/rootfs.ext4 https://s3.amazonaws.com/spec.ccfc.min/img/quickstart_guide/x86_64/rootfs/bionic.rootfs.ext4
```

## Install

```bash
go install github.com/benben/knaller/cmd/knaller@v0.0.1
```

Or download a pre-built binary from the [releases page](https://github.com/benben/knaller/releases).

## CLI Usage

```bash
# Start a microVM (shows logs, prints SSH connection info)
sudo knaller start --name myvm

# Start with custom settings
sudo knaller start --name myvm --cpus 2 --mem 2048

# Connect to the VM (from another terminal)
ssh root@<guest-ip>

# Stop the VM (from another terminal)
sudo knaller stop --name myvm

# List running VMs
sudo knaller list

# List names only
sudo knaller list -q

# Show version
knaller version
```

## Library Usage

### High-level API

```go
package main

import (
    "context"
    "fmt"

    "github.com/benben/knaller"
)

func main() {
    ctx := context.Background()

    // Start a microVM — knaller handles starting Firecracker,
    // configuring the VM, and booting it.
    vm, err := knaller.Run(ctx, &knaller.Config{
        Name:   "myvm",
        Kernel: "/path/to/vmlinux",
        RootFS: "/path/to/rootfs.ext4",
        CPUs:   2,
        Memory: 2048,
    })
    if err != nil {
        panic(err)
    }
    defer vm.Cleanup()

    // Connect via SSH at vm.GuestIP
    fmt.Printf("VM started, SSH to root@%s\n", vm.GuestIP)

    // Block until VM exits
    vm.Wait()
}
```

### Stopping a VM by name

```go
// From a different process:
knaller.StopVM(ctx, "myvm")
```

### Listing VMs

```go
vms, err := knaller.List()
if err != nil {
    panic(err)
}
for _, vm := range vms {
    fmt.Printf("%s: %s (%d vCPUs, %dMiB, ssh root@%s)\n",
        vm.Name, vm.Status, vm.CPUs, vm.Memory, vm.GuestIP)
}
```

### Low-level Firecracker client

For direct access to the Firecracker API (advanced usage):

```go
import "github.com/benben/knaller/firecracker"

client := firecracker.NewClient("/path/to/firecracker.socket")
ctx := context.Background()

client.SetBootSource(ctx, &firecracker.BootSource{
    KernelImagePath: "/path/to/vmlinux",
    BootArgs:        "console=ttyS0 reboot=k panic=1",
})
// ... configure drives, network, etc.
client.StartInstance(ctx)

info, _ := client.GetInfo(ctx)
fmt.Println(info.State) // "Running"

client.StopInstance(ctx) // graceful shutdown
```

## How it works

- **Rootfs isolation**: Each VM gets its own copy of the base rootfs at `~/.local/share/knaller/vms/<name>/rootfs.ext4`, using `cp --reflink=auto` for copy-on-write on supported filesystems (btrfs, xfs).
- **Networking**: TAP devices are created via direct ioctl calls using `golang.org/x/sys/unix` (no `ip` command dependency). Each VM gets a /30 subnet derived deterministically from its name. Connect via SSH.
- **DNS**: The host's DNS servers are auto-configured into the guest's `/etc/resolv.conf` before boot. Works with systemd-resolved, NetworkManager, and static configs.
- **No state files**: VM discovery uses the Firecracker API directly — the socket's existence IS the state. `knaller list` scans the socket directory and queries each running instance.
- **Cleanup**: `vm.Cleanup()` removes the API socket, TAP device, and rootfs copy. The CLI does this automatically on Ctrl+C / SIGTERM.
