#!/usr/bin/env bash
#
# Installs typesafe on the machine this runs on. Not run directly: it is piped
# over SSH by deploy/gcp.sh and by .github/workflows/deploy.yml, both of which
# upload the binary to /tmp/typesafe first.
#
#   gcloud compute ssh $INSTANCE --command "PORT=2222 bash -s" < remote-install.sh
#
# Keeping it in one file is what stops the manual path and the CI path from
# drifting into two subtly different systemd units.

set -euo pipefail

PORT="${PORT:-2222}"

[ -f /tmp/typesafe ] || { echo "no binary at /tmp/typesafe; upload it first" >&2; exit 1; }

id typesafe >/dev/null 2>&1 || sudo useradd --system --no-create-home --shell /usr/sbin/nologin typesafe

# Keep the outgoing binary so a rollback is a copy and a restart. install(1)
# unlinks the destination before writing, so replacing the running executable
# is atomic and never hits ETXTBSY.
if [ -x /usr/local/bin/typesafe ]; then
  sudo cp -p /usr/local/bin/typesafe /usr/local/bin/typesafe.prev
fi
sudo install -m 755 /tmp/typesafe /usr/local/bin/typesafe

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

sudo systemd-analyze verify /etc/systemd/system/typesafe.service
sudo systemctl daemon-reload
sudo systemctl enable typesafe >/dev/null
sudo systemctl restart typesafe
sleep 2
sudo systemctl is-active typesafe

# is-active only says the process survived. The firewall keeps CI from probing
# the port from outside, so prove it is accepting connections from in here.
for i in 1 2 3 4 5; do
  if ss -ltn "sport = :$PORT" | grep -q LISTEN; then
    echo "listening on :$PORT"
    exit 0
  fi
  sleep 1
done

echo "service is active but nothing is listening on :$PORT" >&2
sudo journalctl -u typesafe -n 30 --no-pager >&2
exit 1
