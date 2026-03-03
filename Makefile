ROOTFS_DIR := $(HOME)/.local/share/knaller
ROOTFS := $(ROOTFS_DIR)/rootfs.ext4
CONTAINER_IMAGE := knaller-guest
CONTAINER_NAME := knaller-tmp

.PHONY: build test create-guest clean-guest download-kernel

build:
	go build -o knaller ./cmd/knaller

test:
	go test ./...
	go vet ./...

create-guest:
	podman build -f Containerfile_guest -t $(CONTAINER_IMAGE) .
	podman create --name $(CONTAINER_NAME) $(CONTAINER_IMAGE)
	podman export $(CONTAINER_NAME) -o /tmp/knaller-rootfs.tar
	podman rm $(CONTAINER_NAME)
	mkdir -p $(ROOTFS_DIR)
	truncate -s 2G $(ROOTFS)
	sudo mkfs.ext4 $(ROOTFS)
	$(eval MNTDIR := $(shell mktemp -d))
	sudo mount $(ROOTFS) $(MNTDIR)
	sudo tar xf /tmp/knaller-rootfs.tar -C $(MNTDIR)
	sudo umount $(MNTDIR)
	rmdir $(MNTDIR)
	rm /tmp/knaller-rootfs.tar
	@echo "Rootfs created at $(ROOTFS)"

download-kernel:
	$(eval ARCH := $(shell uname -m))
	$(eval RELEASE_URL := https://github.com/firecracker-microvm/firecracker/releases)
	$(eval LATEST := $(shell basename $$(curl -fsSLI -o /dev/null -w %{url_effective} $(RELEASE_URL)/latest)))
	$(eval CI_VERSION := $(basename $(LATEST)))
	$(eval KERNEL_KEY := $(shell curl -s "http://spec.ccfc.min.s3.amazonaws.com/?prefix=firecracker-ci/$(CI_VERSION)/$(ARCH)/vmlinux-&list-type=2" \
		| grep -oP '(?<=<Key>)(firecracker-ci/$(CI_VERSION)/$(ARCH)/vmlinux-[0-9]+\.[0-9]+\.[0-9]{1,3})(?=</Key>)' \
		| sort -V | tail -1))
	mkdir -p $(ROOTFS_DIR)
	curl -fsSL -o $(ROOTFS_DIR)/vmlinux "https://s3.amazonaws.com/spec.ccfc.min/$(KERNEL_KEY)"
	@echo "Kernel downloaded to $(ROOTFS_DIR)/vmlinux"

clean-guest:
	rm -f $(ROOTFS)
	-podman rmi $(CONTAINER_IMAGE)
