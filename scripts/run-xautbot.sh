#!/bin/sh

set -eu

PROJECT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

if [ -f "$PROJECT_DIR/.env" ]; then
	set -a
	. "$PROJECT_DIR/.env"
	set +a
fi

exec "$PROJECT_DIR/bin/xautbot" -config "$PROJECT_DIR/configs/config.json"
