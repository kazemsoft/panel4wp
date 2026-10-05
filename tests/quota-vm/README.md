# Isolated storage quota drill

From the repository root, run:

```sh
tests/quota-vm/run.sh
```

Requires Docker, internet during image build, 1 GiB free RAM and 2 CPUs. Supports AMD64 and ARM64. Execution uses QEMU software emulation, a Linux kernel with XFS quota support, and two disposable virtual disks. It does not mount host files or the Docker socket and does not run a privileged container. There are two boots from the same disk. Success ends with `QUOTA_DRILL_PASS`; failure exits nonzero.

The drill exercises the actual production read-only probe, shared project limits, existing-data preservation, inheritance, limit changes, distinct filesystems, oversize archive extraction and persistence after guest reboot. It also reproduces project-ID reassignment by UID33 and proves that a lab ioctl filter stops that specific action.

**This is groundwork, not proof that panel4wp assigns safe quotas.** The lab filter permits unrelated syscalls and is not a production seccomp profile. There is no nested Docker daemon or real WordPress/MariaDB stack in the VM. Docker boot gating, writer-container policies, project ownership, interrupted tagging, host volume migration and real site restore remain unverified. See [the resource guide](../../docs/RESOURCES.md#hard-disk-quotas).
