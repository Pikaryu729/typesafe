#!/usr/bin/env bash
#
# Installs typesafe on the machine this runs on. Not run directly: it is piped
# over SSH by deploy/gcp.sh and by .github/workflows/deploy.yml, both of which
# upload the binary first.
#
#   gcloud compute ssh $INSTANCE --command "PORT=2222 bash -s" < remote-install.sh
#
# Keeping it in one file is what stops the manual path and the CI path from
# drifting into two subtly different systemd units.
#
#   SRC   uploaded binary to install; defaults to ~/typesafe.new
#   PORT  port the unit binds; defaults to 2222

set -euo pipefail

PORT="${PORT:-2222}"
# Home-relative, not /tmp: the operator and the CI service account log in as
# different users, and /tmp is sticky, so whoever uploaded there first would
# own a file the other could neither overwrite nor unlink.
SRC="${SRC:-$HOME/typesafe.new}"

case "$PORT" in ''|*[!0-9]*) echo "PORT '$PORT' is not a number" >&2; exit 1 ;; esac
[ -f "$SRC" ] || { echo "no binary at $SRC; upload it first" >&2; exit 1; }

BACKUP=/var/backups/typesafe.prev       # last version known to have come up
CANDIDATE=/var/backups/typesafe.pending # only promoted once this deploy is verified

id typesafe >/dev/null 2>&1 || sudo useradd --system --no-create-home --shell /usr/sbin/nologin typesafe
sudo install -d -m 0755 /var/backups

# Set the outgoing binary aside, but do not call it the rollback target yet. A
# deploy that fails verification must leave the *last good* binary in place to
# roll back to, not the broken one it just replaced.
if [ -x /usr/local/bin/typesafe ]; then
  sudo cp -p /usr/local/bin/typesafe "$CANDIDATE"
fi

fail() {
  echo "$1" >&2
  sudo rm -f "$CANDIDATE"   # keep the older, working $BACKUP as the rollback target
  sudo journalctl -u typesafe -n 30 --no-pager >&2 || true
  exit 1
}

# install(1) unlinks the destination before writing, so replacing the running
# executable is atomic and never hits ETXTBSY.
sudo install -m 755 "$SRC" /usr/local/bin/typesafe
rm -f "$SRC"

sudo tee /etc/systemd/system/typesafe.service >/dev/null <<UNIT
[Unit]
Description=typesafe SSH typing server
After=network-online.target
Wants=network-online.target

[Service]
Type=exec
User=typesafe
Group=typesafe
ExecStart=/usr/local/bin/typesafe -host 0.0.0.0 -port ${PORT} -host-key /var/lib/typesafe/host_ed25519
StateDirectory=typesafe
StateDirectoryMode=0700
Restart=on-failure
RestartSec=2s
KillSignal=SIGTERM
TimeoutStopSec=20s
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectKernelLogs=yes
ProtectControlGroups=yes
ProtectClock=yes
ProtectHostname=yes
ProtectProc=invisible
RestrictAddressFamilies=AF_INET AF_INET6
RestrictNamespaces=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
LockPersonality=yes
SystemCallArchitectures=native
SystemCallFilter=@system-service
CapabilityBoundingSet=
AmbientCapabilities=
UMask=0077

[Install]
WantedBy=multi-user.target
UNIT

sudo systemd-analyze verify /etc/systemd/system/typesafe.service || fail "the unit file is invalid"
sudo systemctl daemon-reload
sudo systemctl enable typesafe >/dev/null
sudo systemctl restart typesafe
sleep 2
sudo systemctl is-active typesafe || fail "the service is not active after restart"

# is-active only says the process survived. The firewall keeps CI from probing
# the port from outside, so prove it is accepting connections from in here.
listening=false
for _ in 1 2 3 4 5; do
  if ss -ltn "sport = :$PORT" | grep -q LISTEN; then listening=true; break; fi
  sleep 1
done
$listening || fail "the service is active but nothing is listening on :$PORT"

# Verified. This binary is now the one worth rolling back to.
if [ -f "$CANDIDATE" ]; then
  sudo mv "$CANDIDATE" "$BACKUP"
fi

echo "listening on :$PORT"
