# 🧨 knaller

knaller ([/ˈknalɐ/](https://de.wiktionary.org/wiki/Knaller)) — a Go library and CLI for running [Firecracker](https://github.com/firecracker-microvm/firecracker) microVMs.

## Features

- start/stop/pause/resume/snapshot microvms
- set limits on CPU, memory, network bandwidth, disk bandwidth and disk IOPS
- start new microvm from an existing snapshot
- two networking modes:
  - **rootless** (default): user-space networking via [pasta](https://passt.top/) — no privileges required
  - **direct**: per-VM kernel network namespace — needed where pasta breaks KVM (e.g. Kubernetes pods that already run in a user namespace)
- adopt running VMs across supervisor restarts (`AdoptVM`) — useful for daemons that outlive their VMs
- raw-disk mode: hand a pre-attached block device to firecracker (NBD, LVM, etc.) and skip the rootfs copy
- raw snapshots: pause/snapshot/resume a VM with a `whilePaused` hook for callers managing the disk lifecycle out of band

## Requirements

- Linux with KVM
- [Firecracker binary](https://github.com/firecracker-microvm/firecracker/releases)
- For rootless mode: [pasta](https://passt.top/)
- For direct mode: `iproute2`, `nftables`, `util-linux` (`nsenter`); `CAP_NET_ADMIN` + `CAP_NET_RAW` in the host netns. `e2fsprogs` is also needed if you use `Config.RootFSSize`.
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

## Direct networking mode

`knaller.RunDirect` is a drop-in replacement for `knaller.Run` that puts the
VM inside a per-VM **kernel** network namespace instead of pasta's user+network
namespace. Use it where pasta isn't viable — most commonly when your supervisor
already runs in a user namespace (Kubernetes pods, rootless containers), since
KVM's `KVM_CREATE_VM` ioctl returns `EPERM` from inside a user namespace.

What direct mode sets up per VM:

- a kernel netns named `kn-<hash>` containing the firecracker TAP device
- a veth pair (`vh-<hash>` host-side / `vg-<hash>` guest-side) with a /30 in
  `172.20.0.0/16`, plumbed on both sides
- an in-netns `knaller_box_nat` nft table that DNATs port 22 (and any
  `Config.Ports`) to the guest IP, and SNATs new outbound flows to the
  veth-guest IP so siblings on the host see distinct sources
- a host-side `knaller_host` nft table that DNATs `host:<sshPort>` (and
  forwarded ports) to the per-VM veth-guest IP, masquerades outbound flows so
  the upstream NIC sees the host's IP, and rejects guest→RFC1918 reachability
  by default (DNS to `169.254.169.253` is allowed; everything else in
  `10/8`, `192.168/16`, `100.64/10`, `224/4`, `169.254/16` and the knaller
  veth supernet itself is rejected)
- the firecracker process is `nsenter`'d into the netns, so `/proc/<pid>/cmdline`
  shows `firecracker` (not `nsenter`) and discovery/adoption can match on the
  command line

Required capabilities and binaries:

- The supervisor must hold `CAP_NET_ADMIN` + `CAP_NET_RAW` in the host
  network namespace. In Kubernetes that means `hostNetwork: true` plus the
  capability set; on a bare host it means root or file capabilities.
- `ip` (iproute2), `nft` (nftables), `nsenter` (util-linux) on `PATH`.

```go
vm, err := knaller.RunDirect(ctx, &knaller.Config{
    Name:   "myvm",
    Kernel: "/path/to/vmlinux",
    RootFS: "/path/to/rootfs.ext4",
    CPUs:   2,
    Memory: 2048,
})
```

### Raw-disk mode

Set `Config.RawDiskPath` to a block device or file the caller manages out of
band (e.g. an NBD device backed by a content-addressed cache, or an LVM
logical volume). knaller does **not** copy, truncate, or resize the device,
and `Cleanup()` leaves it alone — the caller owns the disk lifecycle. This
also disables the per-VM rootfs copy, so VM start time is bounded by the
firecracker handshake instead of by `cp` + `resize2fs`.

```go
vm, err := knaller.RunDirect(ctx, &knaller.Config{
    Name:        "myvm",
    Kernel:      "/path/to/vmlinux",
    RawDiskPath: "/dev/nbd0",
    CPUs:        2,
    Memory:      2048,
})
```

### Adopting a running VM

A long-running supervisor that gets restarted (e.g. a Kubernetes DaemonSet)
can re-attach to VMs from its previous lifetime with `AdoptVM`. Persist
`vm.Name`, `vm.SocketPath`, and `vm.PID` somewhere durable; on restart, call:

```go
vm, err := knaller.AdoptVM(name, socketPath, pid)
```

`AdoptVM` verifies the firecracker is alive (`kill -0 pid`) and that its
API socket still answers (`GetInfo` with a 2 s timeout). The returned `*VM`
has `cmd == nil`; `Wait` switches to polling `/proc/<pid>` and `Kill` falls
back to `syscall.Kill(pid, SIGKILL)`. Pair this with
`Config.EscapeCgroupSlice` to move the firecracker process into a host-level
cgroupv2 slice on launch so it survives the supervisor's container being
restarted.

### Raw snapshots

`CreateSnapshotRaw` is like `CreateSnapshot` but skips the rootfs copy and
the drive-path patching, so it composes with `RawDiskPath`. It returns
timing for the paused-window so callers can attribute pause-tail latency,
and accepts a `whilePaused` callback that runs after the firecracker
state+memory dump is written but before the VM is resumed — useful for
flushing a dirty queue or copying an external manifest into `snapDir`.

```go
res, err := knaller.CreateSnapshotRaw(ctx, "myvm", os.Stderr, func(snapDir string) error {
    return copyManifestInto(snapDir)
})
```

On restore (`RunDirect` with `SnapshotID` + `RawDiskPath`), `LoadSnapshot`
is followed by `PatchDrive` so the new `RawDiskPath` replaces whatever
device path was baked into the state file.