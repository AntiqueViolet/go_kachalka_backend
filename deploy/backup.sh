#!/bin/sh
# Daily pg_dump into /backups (custom format, restore with pg_restore). Connection settings come
# from PG* environment variables. Old dumps are pruned only after a successful dump and the
# newest ones always stay, so failing backups never eat the good ones. A failure is logged and
# turns the container unhealthy (compose healthcheck: the newest dump is older than 26 h).
set -eu

keep_days="${BACKUP_KEEP_DAYS:-14}"
keep_min=3

while true; do
  file="/backups/kachalka-$(date -u +%Y%m%d-%H%M%S).dump"
  if pg_dump --format=custom --file="$file.tmp"; then
    mv "$file.tmp" "$file"
    echo "backup written: $file"
    # Names sort by time: skip the keep_min newest, delete the rest once older than keep_days.
    ls -1 /backups/kachalka-*.dump | sort -r | tail -n +$((keep_min + 1)) | while read -r old; do
      if [ -n "$(find "$old" -mtime +"$keep_days")" ]; then
        rm -f "$old" && echo "backup pruned: $old"
      fi
    done
  else
    rm -f "$file.tmp"
    echo "backup FAILED" >&2
  fi
  find /backups -name 'kachalka-*.dump.tmp' -mmin +60 -delete
  sleep 86400
done
