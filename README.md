# 🧨 knaller

knaller ([/ˈknalɐ/](https://de.wiktionary.org/wiki/Knaller)) — a Go library and CLI for running [Firecracker](https://github.com/firecracker-microvm/firecracker) microVMs.

## Features

- start/stop/pause/resume/snapshot microvms
- set limits on CPU, memory, network bandwidth, disk bandwidth and disk IOPS
- start new microvm from an existing snapshot
- rootless operation (user space networking with passt)

## Requirements

- Linux with KVM
- [Firecracker binary](https://github.com/firecracker-microvm/firecracker/releases)
- [pasta](https://passt.top/) for rootless user space networking
- Podman for building the guest rootfs image

## Install

CLI:
```
go install github.com/benben/knaller/cmd/knaller@v0.0.1
```

Library:
```
go get github.com/benben/knaller
```

## Quick start

```
sudo pacman -S passt
make create-guest
make download-kernel
knaller start --name my-vm
```
copy pasta the ssh command into another terminal window and connect with password `root`.

## CLI Usage

```bash
$ knaller help
Usage: knaller <command> [flags]

Commands:
  start             Start a microVM (connect via SSH)
  stop              Stop a running microVM
  rm                Remove a stopped microVM
  pause             Pause a running microVM
  resume            Resume a paused microVM
  snapshot          Create a VM snapshot
  snapshot ls       List all snapshots
  snapshot delete   Delete a snapshot
  list              List running microVMs
  ls                Alias for list
  version           Print version information
  help              Show this help

Use knaller <command> --help for more information about a command.
```

### `knaller start`

```
$ knaller start --help
Usage of start:
  -cpus float
        vCPUs (e.g. 0.5 = 1 vCPU at 50% CPU quota) (default 1)
  -detach
        Run VM in the background
  -disk-bandwidth int
        Disk bandwidth limit in MB/s (0 = unlimited)
  -disk-iops int
        Disk I/O operations per second limit (0 = unlimited)
  -firecracker string
        Firecracker binary path (default "firecracker")
  -from-snapshot string
        Restore from snapshot ID
  -kernel string
        Kernel image path (default "~/.local/share/knaller/vmlinux")
  -mem int
        Memory in MiB (default 1024)
  -name string
        VM name (required)
  -network-bandwidth float
        Network bandwidth limit in Mbps per direction (0 = unlimited)
  -pasta string
        pasta binary path (default "pasta")
  -port HOST:GUEST
        Port forwarding HOST:GUEST (repeatable)
  -rootfs string
        Base rootfs path (default "~/.local/share/knaller/rootfs.ext4")
  -verbose
        Show serial console and process output
```

## Library Usage

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

    // Connect via SSH
    fmt.Printf("VM started: ssh -p %d root@localhost\n", vm.Port)

    // Block until VM exits
    vm.Wait()
}
```