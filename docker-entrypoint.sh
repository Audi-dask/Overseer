#!/bin/sh
set -e

# Named volumes are often root-owned; the app runs as uid 1000.
if [ "$(id -u)" = "0" ]; then
	mkdir -p /data/workspaces /data/reviewlogs
	chown -R overseer:overseer /data
	exec su-exec overseer:overseer overseer "$@"
fi

exec overseer "$@"
