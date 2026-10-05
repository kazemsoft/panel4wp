# Resources and capacity

Open a site's **Resources** page to change its plan or choose custom RAM and CPU values. The same limits apply separately to WordPress and MariaDB; the site's total allocation is twice the selected value. Presets are Small (384 MiB / 0.5 CPU), Standard (768 MiB / 1 CPU), and Large (1536 MiB / 2 CPUs), per container. Custom values allow 256–8192 MiB and 0.25–8 CPUs in steps of 0.25. Custom values are saved with the site. Settings also supports up to 30 reusable named plan templates; these appear in the plan selector on new-site and Resources pages. Deleting a template does not change the allocations of existing sites. Templates are stored in `data/panel/resource-plans.json`; include it in installation-data backups.

## Applying changes

- Running sites briefly stop while their two containers are recreated with the saved limits. Their existing named volumes, database credentials, routes, files, and backups are preserved.
- Stopped sites have their containers recreated without starting them. Start later applies the same saved limits.
- The database manager session is closed before recreation and must be reopened afterward.
- Each change uses a durable operation receipt. Duplicate mutations remain blocked until its result is resolved.
- The worker saves the desired Compose configuration atomically before changing containers. After a crash, reconciliation imports these configured limits into the panel. A mismatch with actual Docker limits produces a service warning; explicit Start recreates the services with the saved configuration. Interrupted resource changes stop both services before recovery is offered.
- Requests rejected during admission leave the site's configuration and service state unchanged.

## Admission checks

The new-site and Resources pages show a capacity snapshot. The worker checks again immediately before provisioning, changing resources, or starting a site, while mutations are serialized. These checks cannot be bypassed by editing browser form fields.

The budget uses Docker's total RAM and CPU. Docker Desktop therefore reports the capacity of its Linux VM, not the physical Mac's full capacity. The host reserve is the larger of 512 MiB and 20% of RAM, plus 0.5 CPU.

Managed site reservations include both services, including stopped sites and orphaned managed containers. The larger of the saved and actual Docker limits is counted. The edited site's previous allocation is replaced by its requested allocation. An unfamiliar Compose resource configuration blocks admission for inspection rather than silently dropping its reservation.

Other running Docker containers, including database tools, are also counted. A configured RAM/CPU limit is reserved in full; for an unlimited resource, current Docker stats usage is subtracted instead. This sampled usage can grow afterward. Non-Docker host workloads and future externally created containers cannot be guaranteed by this budget: use a dedicated host and retain headroom. This is admission budgeting, not a promise of continuously free RAM or reserved CPU execution time.

At least 2 GiB must be free on the Docker storage filesystem and on the panel data filesystem. The Docker check uses the container filesystem on the default local Docker storage; external volume drivers or independently mounted volume storage are outside this check. Capacity readings fail closed when Docker, statistics, or storage cannot be checked.

## Hard disk quotas

Disk admission and disk usage reporting are **not hard per-site quotas**. WordPress and database data live in Docker volumes. A container's writable-layer `storage-opt size` does not limit those volumes. Filesystem-backed project quotas or a supported volume driver must enforce the limit; see [Docker volume documentation](https://docs.docker.com/engine/storage/volumes/) and [Docker storage options](https://docs.docker.com/reference/cli/dockerd/).

The Resources page now reports the backing filesystem of each site's two volumes and checks project-quota accounting and enforcement through Linux `quotactl_fd(Q_XGETQSTATV)`. Both volumes must be Compose-owned local volumes without driver options, on the same XFS filesystem. The query requires Linux 5.14 or later and suitable worker-helper permissions. A filesystem type or mount option alone is not treated as proof of enforcement. A failed query remains unverified.

This is a **read-only capability check**. Even when XFS reports enforcement enabled, no project quota has been assigned by the panel. The UI always says **Not enabled**. The current Docker Desktop installation uses ext4, and its VM kernel also lacks XFS quota support; activation cannot be tested directly on that storage.

### Isolated Linux proof

Run the reproducible kernel drill from the repository root:

```sh
tests/quota-vm/run.sh
```

The script builds an Alpine Linux VM with XFS quota support and boots it through QEMU. It works with AMD64 and ARM64 Docker hosts and does not require the host kernel to support XFS quotas. The outer container has no host mounts, Docker socket, device access, privileged flag, or network during execution. Its two sparse test disks are discarded with the container. Building requires internet access for Alpine packages; execution reserves 1 GiB RAM and 2 CPUs. The final success marker is `QUOTA_DRILL_PASS`; a VM failure or timeout makes the script fail. CI also runs this drill.

Verified on local ARM64 Docker on 2026-10-05:

- Native kernel quota enforcement limits the **combined** WordPress and database directory usage, rather than each directory independently.
- Project tagging preserves an existing file, and new subdirectories inherit the quota.
- Increasing the limit permits additional writes; lowering it after removing data restores the smaller limit.
- Two different XFS filesystems are rejected as a shared-quota backend by the production probe.
- The limit and seed file survive a real guest shutdown and second boot from the same virtual disk. Mounting without quota enforcement is reported as unsupported/unverified.
- An oversized archive extraction fails at the quota while a separate safety file remains intact. This is a filesystem test, **not a WordPress/MariaDB restore acceptance test**.
- A process running as UID33 can change the project ID of its own file and write 16 MiB outside an 8 MiB project limit. A lab seccomp filter blocks that ioctl. This deliberately reproduces why filesystem quotas alone are insufficient.

### Activation gates still open

1. Protect every writer (WordPress, MariaDB, CLI, restore and file helpers) against changing project IDs or inheritance. Preserve Docker's default seccomp protections while denying quota attribute mutation, including applicable newer attribute syscalls. The small filter in the drill permits unrelated syscalls and **must not be used as a production profile**.
2. Gate automatic Docker/host restarts on verified quota enforcement. Existing `unless-stopped` policies can restart containers before the worker checks storage; quota-enabled sites must not use that path without a verified gate.
3. Allocate project IDs durably, verify filesystem-global ownership/collisions, tag existing data with every writer stopped, and recover partial tagging after interruption. Shared hardlinks, symlinks and unrelated projects need explicit acceptance tests.
4. Test real WordPress/MariaDB activation, limit reductions below usage, backup/restore at capacity, interrupted operations, and failure recovery on a dedicated Linux Docker host. Preserve safety backups and leave failed restores stopped.
5. Document and test moving existing ext4 volumes to quota-capable storage, with rollback and data-preservation checks. No host filesystem migration or volume retagging is performed by this release.

Primary references: [XFS quota manual](https://man7.org/linux/man-pages/man8/xfs_quota.8.html), [Linux quota syscall](https://man7.org/linux/man-pages/man2/quotactl.2.html), [Linux owner attribute checks](https://github.com/torvalds/linux/blob/v6.6/fs/ioctl.c), [Docker restart policies](https://docs.docker.com/engine/containers/start-containers-automatically/).

Hard quotas remain open in roadmap step 2 until these gates pass.

## Verification

Automated tests cover invalid/non-finite values, both-container accounting, host reserves, stopped-site reservations, other Docker workloads, capacity rejection without service changes, preservation of volumes, stopped-state preservation, durable receipts, and lost-response reconciliation. Live Docker verification is recorded after each resource-change drill. Full host reboot and Linux filesystem quota acceptance remain separate checks.

The live Docker drill on 2026-10-05 passed named-plan creation/application/deletion, custom provisioning, a running resource change, a preset change, a stopped resource change, and over-capacity rejection. Docker inspection confirmed both containers' real RAM/CPU limits. An uploaded file and a database table row survived each recreation. The drill then persisted a new Compose target and a pending receipt, killed and explicitly restarted the worker, and confirmed that reconciliation detected the mismatch. Explicit Start applied the target limits while preserving the same file and database row. All disposable sites, volumes, backups, and test templates were removed; the existing demo remained running.

The drill also exposed and fixed readiness during initial provisioning: WP-CLI now waits for the completed `wp-config.php` bootstrap as well as WordPress core files. Docker runtime inspection handles containers with no healthcheck. A shell regression test delays the configuration write and verifies that WP-CLI does not run early.
