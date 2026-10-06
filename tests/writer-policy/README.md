# Production writer profile control test

From the repository root:

```sh
tests/writer-policy/run.sh
```

Docker builds a small static Linux probe. It runs twice with disposable local volumes, no host mounts/socket, no network, and limited resources. The first run uses Docker's default profile; the UID33 inode owner can set its own unchanged project attributes. The second uses the exact profile embedded in the worker; sensitive project/file flag mutation commands must fail with EPERM, including arbitrary high-word/sign-extension variants. Ordinary read, write, fsync and a pipe `FIONREAD` ioctl must still succeed. Expected markers: `WRITER_POLICY_CONTROL_PASS` and `WRITER_POLICY_PASS`. Volumes and the test image are removed afterward.

Supported and tested architectures are AMD64 and ARM64. The symbolic Go test proves that the generated low32 prefix partition covers exactly all other ioctl commands and preserves unrelated rules from the pinned Moby baseline. Pinned provenance and license are in `third_party/moby/README.md`.

This proves container filter enforcement, not quota allocation. The XFS kernel drill and real quota-enabled WordPress/MariaDB lifecycle remain separate acceptance gates in `docs/RESOURCES.md`.
