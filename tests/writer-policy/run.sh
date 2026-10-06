#!/bin/sh
set -eu
repo=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo"
# Test only fresh disposable volumes; no host mounts, socket or privileged mode.
suffix="$$-$(date +%s)"
image="panel4wp-writer-check:$suffix"
control="panel4wp-writer-control-$suffix"
guarded="panel4wp-writer-guarded-$suffix"
cleanup() {
 docker volume rm "$control" "$guarded" >/dev/null 2>&1 || true
 docker image rm "$image" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM
docker build -f tests/writer-policy/Dockerfile -t "$image" .
docker volume create "$control" >/dev/null
docker volume create "$guarded" >/dev/null
docker run --rm --network none --memory 32m --cpus .25 --pids-limit 16 \
 --security-opt no-new-privileges:true -v "$control:/data" "$image" control
docker run --rm --network none --memory 32m --cpus .25 --pids-limit 16 \
 --security-opt no-new-privileges:true --security-opt "seccomp=$repo/internal/securitypolicy/writer-v1.json" \
 -v "$guarded:/data" "$image" blocked
