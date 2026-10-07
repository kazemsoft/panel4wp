FROM golang:1.26-alpine AS registry-check
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go test -c -ldflags="-s -w" -o /quota-registry-check ./cmd/worker

FROM alpine:3.22 AS helpers
RUN apk add --no-cache build-base linux-headers xfsprogs-dev util-linux-dev
COPY deploy/storage-probe.c /src/storage-probe.c
COPY deploy/storage-inventory.h deploy/storage-common.h deploy/storage-authority.c deploy/storage-tagging.h /src/
COPY tests/quota-vm/owner-check.c /src/owner-check.c
RUN cc -std=c11 -Wall -Wextra -Werror -O2 -static /src/storage-probe.c -o /storage-probe && cc -std=c11 -Wall -Wextra -Werror -O2 -static -DINVENTORY_MAX_INODES=3 /src/storage-probe.c -o /storage-probe-small && cc -std=c11 -Wall -Wextra -Werror -O2 -static /src/storage-authority.c -o /storage-authority && cc -std=c11 -Wall -Wextra -Werror -O2 -static -DAUTHORITY_TEST_CHECKPOINTS /src/storage-authority.c -o /storage-authority-crash && cc -std=c11 -Wall -Wextra -Werror -O2 -static -DPLAN_MAX_SITES=2 /src/storage-authority.c -o /storage-authority-small && cc -std=c11 -Wall -Wextra -Werror -O2 -static /src/owner-check.c -o /owner-check

FROM alpine:3.22
RUN case "$(uname -m)" in aarch64) task_qemu=aarch64 ;; x86_64) task_qemu=x86_64 ;; *) exit 1 ;; esac; apk add --no-cache "qemu-system-$task_qemu" linux-virt xfsprogs xfsprogs-extra cpio gzip jq docker-engine docker-cli
COPY --from=helpers /storage-probe /usr/local/bin/storage-probe
COPY --from=helpers /storage-authority /usr/local/bin/storage-authority
COPY --from=helpers /storage-authority-crash /usr/local/bin/storage-authority-crash
COPY --from=helpers /storage-authority-small /usr/local/bin/storage-authority-small
COPY --from=helpers /storage-probe-small /usr/local/bin/storage-probe-small
COPY --from=helpers /owner-check /usr/local/bin/owner-check
COPY --from=registry-check /quota-registry-check /lab/quota-registry-check
COPY tests/quota-vm/init tests/quota-vm/drill tests/quota-vm/vm-run tests/quota-vm/daemon-check tests/quota-vm/authority-check tests/quota-vm/tagging-check /lab/
RUN cp /lab/init /init && chmod +x /init /lab/* && mkdir -p /proc /sys /dev /quota /other /quota-wordpress /quota-database && find . -xdev -not -path './proc/*' -not -path './sys/*' -not -path './dev/*' -not -path './tmp/*' -not -path './boot/*' -not -path './usr/bin/qemu*' -not -path './usr/lib/qemu/*' -not -path './usr/lib/python*' | cpio -o -H newc | gzip > /tmp/root.cpio.gz
RUN truncate -s 512M /tmp/quota.img && mkfs.xfs -f /tmp/quota.img >/dev/null && truncate -s 512M /tmp/other.img && mkfs.xfs -f /tmp/other.img >/dev/null
ENTRYPOINT ["/lab/vm-run"]
