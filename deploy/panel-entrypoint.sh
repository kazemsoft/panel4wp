#!/bin/sh
set -eu
chown panel:panel /data
exec su-exec panel /usr/local/bin/panel "$@"
