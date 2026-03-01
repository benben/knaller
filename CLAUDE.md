# knaller

## What is this?

knaller is a Go library and CLI that makes it simple to run Firecracker microVMs.
It provides a high-level API (`knaller.Run`, `knaller.List`, `vm.Stop`, `vm.Cleanup`)
that abstracts away the low-level Firecracker socket API. There's also a low-level
`firecracker` sub-package for direct API access.

## Project layout

```
knaller/
  vm.go              High-level API: Run(), List(), VM type
  config.go          Config struct with defaults and validation
  network.go         TAP device creation/teardown via ioctl (no ip command)
  disk.go            Per-VM rootfs copy management + DNS auto-config
  firecracker/
    client.go        Low-level HTTP-over-Unix-socket Firecracker API client
    models.go        Firecracker API types (BootSource, Drive, etc.)
  cmd/knaller/
    main.go          CLI binary entry point + subcommand dispatch
  internal/cli/
    run.go           "knaller run" subcommand
    list.go          "knaller list" subcommand
    version.go       "knaller version" subcommand (version set via ldflags)
```

## Key design decisions

- **No state files.** VM discovery is done by scanning a socket directory
  (`$XDG_RUNTIME_DIR/knaller/` or `/tmp/knaller/`) and querying each Firecracker
  instance via its API. The socket's existence IS the state.

- **Pure Go networking.** TAP devices are created using ioctl syscalls via
  `golang.org/x/sys/unix`. No shelling out to `ip` or `brctl`.

- **Persistent TAP devices.** We use TUNSETPERSIST so Firecracker can open the TAP
  by name. Our process creates the TAP, marks it persistent, closes the fd, and
  Firecracker opens it independently. Cleanup reverses this: reopen, clear persist, close.

- **Per-VM rootfs copies.** Each VM gets its own copy of the base rootfs at
  `~/.local/share/knaller/vms/<name>/rootfs.ext4`, using `cp --reflink=auto`
  for copy-on-write when the filesystem supports it.

- **Auto DNS.** Before booting, we mount the rootfs copy and write /etc/resolv.conf
  using the host's DNS servers. We skip localhost entries (systemd-resolved stub)
  and fall back to `resolvectl dns` output or 1.1.1.1/8.8.8.8.

- **Cleanup is explicit.** Call `vm.Cleanup()` after `vm.Wait()` returns. This removes
  the TAP device, rootfs copy, and API socket. If you forget, TAP devices and disk
  copies will leak. The CLI handles this automatically via signal handlers.

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

- `golang.org/x/sys/unix` — TAP ioctl constants and types (quasi-stdlib from the Go team)
- No other external dependencies

## Requirements for running VMs

- Linux with KVM (`/dev/kvm`)
- Root access (TAP devices, mounting rootfs)
- `tun` kernel module loaded (`modprobe tun`)
- IP forwarding enabled: `sysctl -w net.ipv4.ip_forward=1`
- iptables NAT rules for guest internet access (see README)
- Firecracker binary (linux/amd64 or linux/arm64)

## Common issues

- **"Resource busy" when Firecracker starts:** The TAP device fd was not closed before
  Firecracker tried to open it. This was fixed by using TUNSETPERSIST.
- **Guest DNS doesn't work:** The host's /etc/resolv.conf pointed to 127.0.0.53
  (systemd-resolved). We detect this and use `resolvectl dns` to find real upstreams.
- **apt interactive prompts in guest:** Use `DEBIAN_FRONTEND=noninteractive` and
  dpkg force-confdef/force-confold options.
