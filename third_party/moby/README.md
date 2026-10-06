# Moby seccomp baseline

`seccomp-default.json` is unmodified from [moby/profiles](https://github.com/moby/profiles/tree/2ceae35d351c156cb5a8efc0fdc4a08cf94569d8), commit `2ceae35d351c156cb5a8efc0fdc4a08cf94569d8`, downloaded 2026-10-06. The upstream Apache 2.0 license is included in `LICENSE` and applies to this baseline and its derived profile.

Run `go generate ./internal/securitypolicy` to reproduce `internal/securitypolicy/writer-v1.json`. The derivation removes allowance for quota control and the newer file attribute setter, then replaces the generic ioctl allowance with masked prefix rules permitting every low-32-bit command except project/file-flag mutation ioctls. Blocked commands fall through to the baseline EPERM default. It retains the baseline's default action, architecture map and other rules. Ioctl commands are matched on their low 32 bits, as the kernel does. This version of the baseline is pinned; updating it requires review and real container compatibility tests.
