# knaller

knaller ([/ˈknalɐ/](https://de.wiktionary.org/wiki/Knaller)) — a Go library and CLI for running [Firecracker](https://github.com/firecracker-microvm/firecracker) microVMs with a container-like experience.

## Requirements

knaller needs a Linux host with KVM support. Firecracker only runs on Linux (x86_64 and aarch64).

### System requirements

- **Linux with KVM** — `/dev/kvm` must be accessible
- **Firecracker binary** — download from [Firecracker releases](https://github.com/firecracker-microvm/firecracker/releases)
- **pasta** (from [passt](https://passt.top/)) — provides rootless networking via user/network namespaces
- **Podman** — needed to build the guest rootfs image (`make create-guest`)

### Guest rootfs

Build the guest rootfs image using the included Containerfile:

```bash
# Build the rootfs (creates ~/.local/share/knaller/rootfs.ext4)
make create-guest
```

This builds an Ubuntu container with openssh-server and systemd, exports it to an ext4 image. The guest is pre-configured with root password `root` and DNS via kernel boot args.

You also need a Linux kernel for Firecracker:

```bash
# Download the latest CI kernel matching your Firecracker version
make download-kernel
```

## Install

```bash
go install github.com/benben/knaller/cmd/knaller@v0.0.1
```

Or download a pre-built binary from the [releases page](https://github.com/benben/knaller/releases).

## CLI Usage

```bash
# Start a microVM (shows logs, prints SSH connection info)
knaller start --name myvm

# Start with custom settings
knaller start --name myvm --cpus 2 --mem 2048

# Connect to the VM (from another terminal, password: root)
ssh root@<guest-ip>

# Stop the VM (from another terminal)
knaller stop --name myvm

# List running VMs
knaller list

# List names only
knaller list -q

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

## Data directory

All knaller data lives under `~/.local/share/knaller/`:

```
~/.local/share/knaller/
  vmlinux              Kernel image
  rootfs.ext4          Base rootfs image (built by make create-guest)
  sockets/             API sockets for running VMs (one per VM)
  vms/<name>/          Per-VM data directories
    rootfs.ext4        VM's own rootfs copy (copy-on-write)
```

## How it works

- **Rootless networking via pasta**: Each VM runs inside a pasta network namespace. pasta (from the passt project) creates a user+network namespace with a TAP device and provides L2/L4 translation to the host — all without root. Inside the namespace, a second TAP is created for Firecracker's guest NIC.
- **Rootfs isolation**: Each VM gets its own copy of the base rootfs at `~/.local/share/knaller/vms/<name>/rootfs.ext4`, using `cp --reflink=auto` for copy-on-write on supported filesystems (btrfs, xfs).
- **DNS**: The host's DNS servers are passed via kernel boot args (`ip=` parameter). The guest's `/etc/resolv.conf` symlinks to `/proc/net/pnp` where the kernel writes them. Works with systemd-resolved, NetworkManager, and static configs.
- **No state files**: VM discovery uses the Firecracker API directly — the socket's existence IS the state. `knaller list` scans the socket directory and queries each running instance.
- **Cleanup**: `vm.Cleanup()` removes the API socket and rootfs copy. The network namespace is cleaned up automatically when pasta exits. The CLI handles this automatically on Ctrl+C / SIGTERM.
