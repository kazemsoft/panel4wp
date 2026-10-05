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

## Hard disk quotas: remaining work

Disk admission and disk usage reporting are **not hard per-site quotas**. WordPress and database data live in Docker volumes. A container's writable-layer `storage-opt size` does not limit those volumes. Filesystem-backed project quotas or a supported volume driver must enforce the limit; see [Docker volume documentation](https://docs.docker.com/engine/storage/volumes/) and [Docker storage options](https://docs.docker.com/reference/cli/dockerd/).

Hard quotas remain in roadmap step 2 until the Linux storage backend, migration of existing volumes, backup/restore behavior at quota limits, and actual out-of-space enforcement can be tested on a suitable VM. The panel does not display an unenforced disk limit as a working quota.

## Verification

Automated tests cover invalid/non-finite values, both-container accounting, host reserves, stopped-site reservations, other Docker workloads, capacity rejection without service changes, preservation of volumes, stopped-state preservation, durable receipts, and lost-response reconciliation. Live Docker verification is recorded after each resource-change drill. Full host reboot and Linux filesystem quota acceptance remain separate checks.

The live Docker drill on 2026-10-05 passed named-plan creation/application/deletion, custom provisioning, a running resource change, a preset change, a stopped resource change, and over-capacity rejection. Docker inspection confirmed both containers' real RAM/CPU limits. An uploaded file and a database table row survived each recreation. The drill then persisted a new Compose target and a pending receipt, killed and explicitly restarted the worker, and confirmed that reconciliation detected the mismatch. Explicit Start applied the target limits while preserving the same file and database row. All disposable sites, volumes, backups, and test templates were removed; the existing demo remained running.

The drill also exposed and fixed readiness during initial provisioning: WP-CLI now waits for the completed `wp-config.php` bootstrap as well as WordPress core files. Docker runtime inspection handles containers with no healthcheck. A shell regression test delays the configuration write and verifies that WP-CLI does not run early.
