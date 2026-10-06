#!/bin/sh
set -eu
task_repo=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
docker build -t panel4wp-quota-drill:local -f "$task_repo/tests/quota-vm/Dockerfile" "$task_repo"
# No host mounts, Docker socket, privileged flag, devices or host network.
# The two sparse test disks exist only inside this disposable container.
docker run --rm --network none --memory 1536m --cpus 2 --pids-limit 128 --cap-drop ALL --security-opt no-new-privileges:true panel4wp-quota-drill:local
