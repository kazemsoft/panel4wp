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

The script builds an Alpine Linux VM with XFS quota support and boots it through QEMU. It works with AMD64 and ARM64 Docker hosts and does not require the host kernel to support XFS quotas. The outer container has no host mounts, Docker socket, device access, privileged flag, or network during execution. Its two sparse test disks are discarded with the container. Building requires internet access for Alpine packages; execution reserves 1.5 GiB RAM and 2 CPUs. The final success marker is `QUOTA_DRILL_PASS`; a VM failure or timeout makes the script fail. CI also runs this drill.

Verified on local ARM64 Docker on 2026-10-05:

- Native kernel quota enforcement limits the **combined** WordPress and database directory usage, rather than each directory independently.
- Project tagging preserves an existing file, and new subdirectories inherit the quota.
- Increasing the limit permits additional writes; lowering it after removing data restores the smaller limit.
- Two different XFS filesystems are rejected as a shared-quota backend by the production probe.
- The limit and seed file survive a real guest shutdown and second boot from the same virtual disk. Mounting without quota enforcement is reported as unsupported/unverified.
- An oversized archive extraction fails at the quota while a separate safety file remains intact. This is a filesystem test, **not a WordPress/MariaDB restore acceptance test**.
- A process running as UID33 can change the project ID of its own file and write 16 MiB outside an 8 MiB project limit. A lab seccomp filter blocks that ioctl. This deliberately reproduces why filesystem quotas alone are insufficient.

### Writer protection

Managed WordPress, MariaDB, phpMyAdmin, WP-CLI and backup/restore volume helpers now use a versioned production seccomp profile plus `no-new-privileges`. File commands and database imports executed inside services inherit the service filter. This is a prerequisite for hard quotas; it does not allocate or enable them.

The profile is derived from the pinned, Apache-licensed [Moby baseline](../third_party/moby/README.md). It preserves unrelated baseline rules and the EPERM default, removes quota-control syscall allowances, and replaces the generic ioctl allowance with the complement of `FS_IOC_FSSETXATTR` and the native/compat `FS_IOC_SETFLAGS` commands. Rules match the low 32 bits because Linux truncates the command. A prefix partition proves that every other ioctl remains allowed. Adding narrow deny rules alongside a generic ioctl allow proved ineffective in the real Docker control test and is deliberately avoided. Unknown newer syscalls stay denied by the baseline default; kernel/API changes require review before updating the policy.

Run the real container control and enforcement test:

```sh
tests/writer-policy/run.sh
```

It uses two disposable Docker volumes, without a host mount, socket, privilege flag or network during execution. An inode owner at UID33 can set its own attributes under Docker's default profile; the production profile returns EPERM for the forbidden commands and high-word/sign-extension variants. Ordinary read/write/fsync and an unrelated pipe ioctl still succeed. CI runs this test on AMD64; local ARM64 also passed. The smaller filter in the quota VM drill is only a lab filter and is never installed on managed sites.

On worker startup, known saved Compose configurations are upgraded atomically; live containers and site data are preserved. Runtime inspection checks Docker's actual profile content and no-new-privileges for both WordPress and MariaDB. Existing services without the filter show a warning. Explicit **Start** recreates them using their existing volumes and briefly interrupts a running site. No profile activation occurs just because the worker restarts. Unfamiliar custom security configurations are left intact and require administrator inspection. Old sites without phpMyAdmin are supported.

For a rollback while quotas remain disabled, retain the generated profile files and the site's Compose file: the preceding worker version can still start this standard Docker Compose configuration. Restore an installation-data backup if rolling back desired configuration; do not remove Docker volumes. No volume migration or project retagging occurs in this upgrade. A future quota-enabled rollback must separately verify enforcement and cannot rely on this procedure.

### Verified restart gate

Sites with a trusted internal quota record use `restart: "no"` for WordPress and MariaDB, both in saved Compose and actual Docker containers. phpMyAdmin also has no automatic restart. A serialized worker supervisor runs at startup and every 30 seconds. It reads durable running/stopped/blocked intent and operation receipts before deciding whether to resume. Missing intent defaults to stopped. Failed, pending or interrupted mutations block automatic recovery. A certified resource admission rejection with no service effects preserves the previous intent; it is not treated as an interrupted mutation.

Before a writer can start, the read-only native helper checks the actual XFS UUID, both roots' saved project ID and inheritance flag, active accounting/enforcement, and the project's exact hard limit and usage. Wrong, missing or unverified expectations block startup and fence owned services. Container inspection also requires the current writer security profile and `restart=no`. Fencing persists blocked intent, disables actual Docker restart, removes owned jobs and stops services, even if the caller has disconnected. A metadata-write or Docker failure is surfaced rather than interpreted as permission to start.

Explicit Start performs these checks and persists running intent only after healthy service creation. Stop persists stopped intent. Indirect starts through phpMyAdmin, updates, restore preparation, restore and resource changes use the same checks. File/mail writes and backup execution also verify the guard. Restore preparation persists maintenance intent: only the verified database may remain running between requests, WordPress stays stopped, and worker startup fences this state. Successful restore restores running intent; failed operations stay blocked. An administrator must repair the underlying storage/record and explicitly Start a blocked site.

This is a reserved internal protocol for the future allocator, not an activation feature. Do not create or edit quota records to enable quotas. The gate trusts the future allocator to have verified all existing descendants and filesystem-global project ownership before writing a ready record; checking root attributes alone does not prove either. There is no quota allocation API/UI, and ordinary sites retain their current restart policies.

The isolated VM drill now runs its own Docker daemon and two tiny canaries. After a real guest reboot with XFS enforcement disabled, the `restart=no` canary stays down while an `unless-stopped` control resumes; the native helper rejects the unenforced filesystem. This proves Docker policy behavior and the native gate independently. Worker tests prove their orchestration; a real quota-enabled WordPress/MariaDB reboot is still part of the activation acceptance below. The guest daemon never accesses the host Docker socket.

Rollback: non-quota sites retain the preceding release's compatibility. Once quota records exist, never downgrade to a worker without this gate or remove the marker as a recovery action: an older worker could restore an unsafe automatic restart path. Keep site volumes, security profiles, quota records and intent together in installation-data backups; repair or migrate using a version that understands the protocol.

### Provisional project reservations and ownership preflight

The worker now has an internal reservation method backed by `data/sites/.quota/projects.json`, the root-level `.quota-initialized` sentinel and `.quota-lock`. This is foundation for a future allocator; no API/UI calls it, and it does not tag files, set limits or create a ready record. One shared allocator/state root per XFS filesystem UUID is required. The preparation protocol below adds exclusive filesystem authority; provisional reservations alone do not authorize mutations.

A persistent process lock covers initialization, a fresh native inventory and an atomic, directory-fsynced ledger update. Reservations bind a site ID to an XFS UUID and a nonzero project ID. Occupied IDs are skipped, including IDs above the signed 32-bit range. Deleted sites' reservations stay burned. An existing reservation is returned only after its UUID and absence from the current occupied-ID list are reverified. An occupied retry requires ownership recovery; it is never silently adopted or reassigned.

The read-only helper enumerates filesystem-global ordinary allocated inodes through modern XFS bulkstat and initialized project quota records through `Q_XGETNEXTQUOTA`, including projects with limits but no site files. It audits both data trees using descriptor-relative opens with `NO_XDEV` and `NO_SYMLINKS`. Symlinks, regular files with multiple hardlinks, special files, nested mounts (including same-filesystem bind mounts), overlapping roots and existing nonzero project attributes reject the request. Scans have inode, project, recursion and time bounds. Errors, malformed responses and scan limits return no usable project-ID list. This is a live scan, not an atomic filesystem snapshot: the tagging transition must fence every writer and recheck ownership immediately before mutations. Trusted host root remains outside the security boundary.

The ledger rejects duplicate keys, missing schema fields, conflicting reservations, unexpected versions and non-regular metadata. Missing ledger/sentinel or interrupted initialization fails closed. The sentinel lives outside `.quota`, so losing that directory cannot reset burned IDs. Back up the complete sites state root, including these hidden files; never reset the ledger or remove the lock as recovery. The lock inode must remain stable. A previous worker can ignore this provisional state while quotas remain disabled, but it must be preserved for a later upgrade.

The isolated guest test covers multi-page inode enumeration, unrelated projects, limit-only projects, unsafe trees, bounded-scan rejection and unchanged seed data. It also runs the actual worker reservation method against guest Docker volumes and the production native helper, then verifies ledger persistence and rejection without ledger changes after a real guest reboot with enforcement disabled. Unit tests cover concurrent worker instances, retries, deleted-site IDs, corrupted/incomplete metadata and occupied-ID retries. Native partial-tag retry is described below; real quota-enabled WordPress/MariaDB acceptance remains pending.

### Exclusive filesystem authority and preparation journal

An internal preparation method now stops owned services/jobs, persists blocked intent, sets saved and actual Docker restart to `no`, and checks for other running containers using either named volume. It then invokes the fixed `storage-authority` helper against an explicitly bound **dedicated XFS filesystem root**. This binding is reserved internal worker state; no environment switch, command, API or UI enables it. The helper proves the actual filesystem root using XFS bulkstat, verifies enforcement and filesystem identity, and holds one exclusive filesystem lock throughout the fresh census, tree audit and journal publication.

Hidden root metadata consists of `.panel4wp-quota-lock`, `.panel4wp-quota-owner`, `.panel4wp-quota-burned` and `.panel4wp-quota-plans/`. Ownership binds the random installation identity, Docker daemon identity, installation state path, XFS UUID, and inode/generation of the lock, index and plan directory. Another installation or daemon cannot adopt it. The independent burned-ID index is fsynced before a journal is published; every journal must match exactly one index entry. Missing/replaced lock, directory, index or journal fails closed, including a different-site claim after journal loss. An interrupted initialization or index/journal write can require manual recovery; it is never automatically reset.

Each journal snapshots both data trees' inode, generation, original flags, mode and tree identity. It allows at most 8192 inodes per site and 4096 sites per authority; exceeding bounds refuses preparation. Retries verify the complete unchanged snapshot. Symlinks, nonzero project IDs, special files, multiple hardlinks, nested mounts and realtime allocations are rejected. Root metadata requires root ownership and restrictive permissions. Trusted host root can still bypass the protocol or roll back all metadata together; this is not protection against a hostile host administrator.

The `--prepare` helper mounts the two volume views read-only and writes only its fixed authority metadata through the root binding. **It does not tag inodes, set quota limits or write a ready record.** Local `quota.json` remains pending and `quota-plan.json` records pending/prepared state. Either marker fences startup and writer operations, even if its counterpart or the dedicated-root binding is missing. Prepared sites remain stopped. Ordinary sites are unchanged.

Recovery must preserve the complete installation state **and** the hidden filesystem-root authority together. Never delete/recreate locks, remove journals or clear pending markers to resume a site. Do not downgrade prepared sites to a worker that does not recognize preparation markers. See [recovery guidance](RECOVERY.md#future-quota-managed-sites).

The isolated VM tests the production helper and actual worker method, different owner/daemon/state identities, wrong/unsafe roots, repeated preparation, missing/replaced metadata, malformed counts, reservation collisions and bounds. Lab-only variants terminate the helper at lock, owner, burned-index, journal-file and journal publication checkpoints; seed bytes and absence of assigned project IDs are verified afterward. A guest reboot preserves journals, and preparation refuses disabled enforcement or missing root binding while writers remain stopped. This proves durable **preparation**, not recovery after partially tagging a tree. Fault injection binaries are excluded from the production worker image.

### Native tagging and partial-operation retry

The internal tagging transition requires the matching prepared journal and pending launch fence. It persists `tagging` before launching the native helper, and `tagged` only after verification succeeds. Both states remain blocked: `quota.json` stays pending with no hard limit or ready marker. No public command, environment switch, API or UI exposes this transition yet.

Under the same filesystem authority lock, the helper validates every journal against the burned-ID index and rejects overlapping journaled inodes. It pins all current tree descriptors and requires exact inode/generation/mode membership. Each inode must have either its original project-zero attributes or exactly the journal's destination project and expected flags. Directory inheritance is added; other flags and freshly read extent fields are preserved. Setuid/setgid, immutable/append-only and realtime attributes are refused before mutations.

A fresh filesystem-global census must match exactly the journal's currently tagged subset. An unrelated inode using the destination project refuses recovery. Quota inode and block accounting must match that subset after flushing delayed allocation; target and project-zero default soft/hard limits must all be zero. Query errors, bounds and accounting mismatches leave the operation blocked. Each attribute change is fsynced. A final path walk and global ownership/accounting check are required before reporting success. Partial changes are never automatically undone or adopted from a counter alone.

Only the fixed native tagging helper receives writable volume views and `FOWNER`, in addition to the storage helper capabilities. It uses an owned `wph-job-<site>` name and job/site labels, so independent cancellation cleanup and worker startup remove an orphaned helper before retry or other mutations. The container still has no network, a read-only root image, bounded memory/CPU/processes and an explicit descriptor limit.

This is recoverable **tagging**, not quota activation. Hard-limit writes, ready records, real WordPress/MariaDB activation and restore at capacity remain subsequent acceptance gates. Tagged sites remain stopped, and existing ext4 sites are not migrated or retagged.

### Activation gates still open

1. Writer profiles are implemented and container enforcement is tested. Repeat the full writer lifecycle **on actual quota-enabled storage**, including new kernel attribute APIs, as part of the real-site activation gate below.
2. The restart gate is implemented, including durable intent, malformed-record fencing, actual Docker restart checks, native XFS identity/limit verification and a guest Docker restart control. Repeat the combined worker + real WordPress/MariaDB acceptance on assigned quota storage in gate 4.
3. Provisional durable reservations and read-only filesystem-global/tree audits are implemented and tested. Exclusive filesystem authority, writer fencing and durable pre-mutation inode journals, native tagging and journal-driven partial-tag retries are implemented. Remaining: verified hard-limit activation and the real-site acceptance in gate 4. Do not activate from a reservation alone.
4. Test real WordPress/MariaDB activation, limit reductions below usage, backup/restore at capacity, interrupted operations, and failure recovery on a dedicated Linux Docker host. Preserve safety backups and leave failed restores stopped.
5. Document and test moving existing ext4 volumes to quota-capable storage, with rollback and data-preservation checks. No host filesystem migration or volume retagging is performed by this release.

Primary references: [Linux quota enumeration implementation](https://github.com/torvalds/linux/blob/v6.12/fs/quota/quota.c), [XFS quota manual](https://man7.org/linux/man-pages/man8/xfs_quota.8.html), [Linux quota syscall](https://man7.org/linux/man-pages/man2/quotactl.2.html), [Linux owner attribute checks](https://github.com/torvalds/linux/blob/v6.6/fs/ioctl.c), [Docker restart policies](https://docs.docker.com/engine/containers/start-containers-automatically/).

Hard quotas remain open in roadmap step 2 until these gates pass.

## Verification

Automated tests cover invalid/non-finite values, both-container accounting, host reserves, stopped-site reservations, other Docker workloads, capacity rejection without service changes, preservation of volumes, stopped-state preservation, durable receipts, and lost-response reconciliation. Live Docker verification is recorded after each resource-change drill. Full host reboot and Linux filesystem quota acceptance remain separate checks.

The live Docker drill on 2026-10-05 passed named-plan creation/application/deletion, custom provisioning, a running resource change, a preset change, a stopped resource change, and over-capacity rejection. Docker inspection confirmed both containers' real RAM/CPU limits. An uploaded file and a database table row survived each recreation. The drill then persisted a new Compose target and a pending receipt, killed and explicitly restarted the worker, and confirmed that reconciliation detected the mismatch. Explicit Start applied the target limits while preserving the same file and database row. All disposable sites, volumes, backups, and test templates were removed; the existing demo remained running.

The drill also exposed and fixed readiness during initial provisioning: WP-CLI now waits for the completed `wp-config.php` bootstrap as well as WordPress core files. Docker runtime inspection handles containers with no healthcheck. A shell regression test delays the configuration write and verifies that WP-CLI does not run early.

The writer-profile live drill on 2026-10-06 passed fresh WordPress/MariaDB provisioning, actual profile inspection, UID33 mutation-denial checks inside both services and phpMyAdmin, upload/download, a database row write, backup, WP-CLI core/plugin/theme updates, and a stopped-site restore. The restored file matched its original bytes and the database table recovered its original row. A worker restart preserved the applied profile and healthy status. Disposable site data was removed afterward. Explicit Start applied the profile to the existing demo; it remained healthy with its original named WordPress/database volumes. Hard quotas remained disabled throughout this drill.

A separate migrated-legacy-site drill on 2026-10-06 removed the old tool definition from a disposable site, restarted the worker and opened phpMyAdmin. The added service used the production writer filter, the tool network appeared exactly once, and `docker compose config --quiet` passed. A regression test also covers an existing tool-network definition. The fixture was removed after the drill.

The restart-gate drill on 2026-10-06 passed native UUID/project/inheritance/limit mismatch checks, persistence across guest reboot, and Docker canary restart behavior with enforcement disabled. Worker regressions cover explicit Start with a pending receipt, lost enforcement, malformed/pending/dangling records, failed inspect/start, cancellation-independent fencing, intent persistence failure, maintenance, and capacity rejection across restart. The local ext4 drill passed ordinary provisioning/upload, malformed and pending record fencing, rejection of a ready record on unsupported storage, explicit Start/phpMyAdmin refusal, and original-file preservation. The disposable site and volumes were removed; the original demo remained running. This verifies rejection and data preservation, not successful quota-enabled site startup.

The reservation/preflight drill on 2026-10-06 passed on local ARM64: multi-page native census, high and limit-only project IDs, bounded partial-scan refusal, unsafe-tree rejection, actual guest Docker-backed reservations and retries, and ledger/seed preservation over guest reboot. Disabled enforcement rejected a reservation without rewriting the ledger. The focused security review fixed acceptance of incomplete ledger JSON; its deleted-site regression prevents identifier reuse. Docker protocol output now separates successful stdout from stderr warnings while retaining failure diagnostics. The updated production worker was installed locally; dashboard, settings and Resources returned successfully, and the original demo retained its running containers, RAM/CPU limits and named volumes. No live project allocation or retagging occurred.

The authority/preparation drill on 2026-10-07 passed on local ARM64: filesystem-global owner locking, independent burned IDs, lost/replaced metadata refusal, bounded journals, native-helper termination at five publication checkpoints, actual guest Docker writer fencing, and journal/seed preservation after a guest reboot. Disabled enforcement and a missing dedicated-root binding kept prepared writers stopped. Full Go race tests, Go vet and the production worker image build passed. A focused security review found and fixed replacement-lock adoption, metadata-loss identifier reuse, count overflow, writable-root ownership and first-marker failure fencing; no remaining important finding was identified in this slice. The worker was updated locally; dashboard, Settings and Resources returned successfully, and the original demo retained its running container IDs, limits and named volumes. No host inode tagging, hard-limit changes or quota activation occurred.

The native-tagging drill on 2026-10-07 passed on local ARM64: wrong tree membership/generation/modes, hardlinks/symlinks, foreign and unrelated target-ID inodes (including a held-open unlinked inode), overlapping journals, lost journals, existing and default limits, and original setuid modes all refused tagging. Five lab termination points covered before/write/fsync/mid/final verification, followed by safe retries with unchanged journal and data checksums, UID/GID and modes. A partial tree was left across a real guest shutdown and recovered after the second boot. Actual guest Docker worker transitions and retries passed with the production helper, along with orphan-job startup cleanup and refusal after enforcement was disabled. Full Go race tests, Go vet and the production worker image build passed; the focused security review reported no remaining important finding. The updated local worker preserved the original demo containers, limits and named volumes, with dashboard, Settings and Resources responding successfully. Host site data was not retagged; no hard limit or ready record was assigned.
