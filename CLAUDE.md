# knaller

## What is this?

knaller is a Go library and CLI that makes it simple to run Firecracker microVMs.
It starts a Firecracker process for each VM, connects to its API socket, configures
the VM, and boots it. VMs are non-interactive — connect via SSH. It provides a
high-level API (`knaller.Run`, `knaller.List`, `knaller.StopVM`, `vm.Cleanup`) that
abstracts away the low-level Firecracker socket API. There's also a low-level
`firecracker` sub-package for direct API access.

## Project layout

```
knaller/
  vm.go              High-level API: Run(), List(), VM type, AdoptVM(), Kill()
  vm_direct.go       RunDirect() — per-VM kernel netns mode (Kubernetes-friendly)
  config.go          Config struct with defaults and validation
  network.go         Network config derivation + pasta namespace setup script
  disk.go            Per-VM rootfs copy management + host DNS detection
  snapshot.go        CreateSnapshot, CreateSnapshotRaw, LoadSnapshot helpers
  Containerfile_guest Guest rootfs container definition (Ubuntu + sshd + systemd)
  Makefile           Build targets: build, test, create-guest
  firecracker/
    client.go        Low-level HTTP-over-Unix-socket Firecracker API client
    models.go        Firecracker API types (BootSource, Drive, etc.)
  cmd/knaller/
    main.go          CLI binary entry point + subcommand dispatch
  internal/cli/
    start.go         "knaller start" subcommand (non-interactive, SSH access)
    stop.go          "knaller stop" subcommand
    list.go          "knaller list" subcommand
    version.go       "knaller version" subcommand (version set via ldflags)
```

## Key design decisions

- **No state files.** VM discovery is done by scanning the socket directory
  (`~/.local/share/knaller/sockets/`) and querying each Firecracker instance via
  its API. The socket's existence IS the state.

- **SSH access only.** VMs don't have an interactive serial console. Firecracker's
  stdin is not connected. Use SSH to interact with the guest. The guest IP is
  printed on start and available via `knaller list`.

- **Two networking modes.** `Run` puts the VM in a pasta-managed user+network
  namespace (rootless, no privileges). `RunDirect` is **not rootless** — it
  puts the VM in a per-VM kernel network namespace and `nsenter`'s firecracker
  into it. The supervisor needs CAP_NET_ADMIN + CAP_SYS_ADMIN + CAP_NET_RAW in
  the host netns (i.e. root or root-equivalent), and also write access to
  `/sys/fs/cgroup` if `EscapeCgroupSlice` is set. The trade-off pays for itself
  inside Kubernetes pods where pasta's user namespace breaks `KVM_CREATE_VM`.
  RunDirect manages two nft tables — `knaller_box_nat` per-netns and
  `knaller_host` shared — for in/out NAT and a default-deny egress filter that
  lets guests reach the internet but blocks the host's RFC1918 neighbours.

- **Per-VM rootfs copies.** Each VM gets its own copy of the base rootfs at
  `~/.local/share/knaller/vms/<name>/rootfs.ext4`, using `cp --reflink=auto`
  for copy-on-write when the filesystem supports it.

- **Auto DNS via kernel boot args.** DNS servers are passed to the guest via the
  kernel `ip=` boot parameter. The guest rootfs symlinks `/etc/resolv.conf` →
  `/proc/net/pnp` where the kernel writes them. Host DNS detection skips localhost
  entries (systemd-resolved stub) and falls back to `resolvectl dns` or 1.1.1.1/8.8.8.8.

- **One Firecracker process per VM.** Firecracker is not a daemon — each process is
  exactly one VM with one API socket. Knaller starts a new Firecracker process for
  each `Run()` call and manages its lifecycle. `AdoptVM(name, socketPath, pid)`
  re-attaches to a process the current binary did not start, for supervisors that
  outlive their VMs (e.g. across container restarts). Pair with
  `Config.EscapeCgroupSlice` to move firecracker into a host-level cgroupv2 slice
  on launch so it survives the supervisor's container being killed.

- **Cleanup is explicit.** Call `vm.Cleanup()` after `vm.Wait()` returns. This removes
  the API socket and rootfs copy. Network namespace cleanup is automatic when the
  pasta process exits. The CLI handles this automatically via signal handlers.

## Building

```bash
go build -o knaller ./cmd/knaller
```

## Testing

```bash
go test ./...             # unit tests (no root needed)
go vet ./...              # static analysis
```

Unit tests use mock HTTP servers over Unix sockets — they don't need Firecracker,
root access, or KVM. Integration testing requires running actual VMs (root + KVM).

## Releasing

Releases are handled by [GoReleaser](https://goreleaser.com/) via GitHub Actions.
Push a tag to trigger a release:

```bash
git tag v0.1.0
git push origin v0.1.0
```

This cross-compiles for linux/amd64 and linux/arm64, generates a changelog
from commit messages, and creates a GitHub release.

## External dependencies

- No external Go dependencies (only stdlib)

## Requirements for running VMs

- Linux with KVM (`/dev/kvm`)
- pasta binary (from the passt project) — rootless networking
- Firecracker binary (linux/amd64 or linux/arm64)
- No root privileges required

## Common issues

- **Guest DNS doesn't work:** The host's /etc/resolv.conf pointed to 127.0.0.53
  (systemd-resolved). We detect this and use `resolvectl dns` to find real upstreams.
- **apt interactive prompts in guest:** Use `DEBIAN_FRONTEND=noninteractive` and
  dpkg force-confdef/force-confold options.
