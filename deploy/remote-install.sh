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

# proxyVersion is pinned: this binary sits between the server and its data, and
# picking up whatever is newest on each deploy is not a property worth having.
PROXY_VERSION="${PROXY_VERSION:-v2.25.0}"
PROXY_URL="https://storage.googleapis.com/cloud-sql-connectors/cloud-sql-proxy/${PROXY_VERSION}/cloud-sql-proxy.linux.amd64"

install_proxy() {
  if [ -x /usr/local/bin/cloud-sql-proxy ] &&
     /usr/local/bin/cloud-sql-proxy --version 2>/dev/null | grep -q "${PROXY_VERSION#v}"; then
    return
  fi
  curl -fsSL "$PROXY_URL" -o /tmp/cloud-sql-proxy
  sudo install -m 755 /tmp/cloud-sql-proxy /usr/local/bin/cloud-sql-proxy
  rm -f /tmp/cloud-sql-proxy
}

write_proxy_unit() {
  # Loopback only. Keeping it TCP rather than a Unix socket is what lets
  # typesafe.service keep RestrictAddressFamilies to the two INET families; a
  # socket would mean allowing AF_UNIX as well.
  sudo tee /etc/systemd/system/typesafe-sqlproxy.service >/dev/null <<UNIT
[Unit]
Description=Cloud SQL Auth Proxy for typesafe
After=network-online.target
Wants=network-online.target

[Service]
Type=exec
User=typesafe
Group=typesafe
ExecStart=/usr/local/bin/cloud-sql-proxy --address 127.0.0.1 --port 5432 ${SQL_CONNECTION_NAME}
Restart=always
RestartSec=2s
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
RestrictAddressFamilies=AF_INET AF_INET6
RestrictNamespaces=yes
LockPersonality=yes
SystemCallArchitectures=native
SystemCallFilter=@system-service
CapabilityBoundingSet=
UMask=0077

[Install]
WantedBy=multi-user.target
UNIT
}

write_dbenv_unit() {
  # Why a separate unit rather than an ExecStartPre on typesafe.service:
  # systemd reads EnvironmentFile when the service starts, before that
  # service's own ExecStartPre runs, so a file written there would be read too
  # late. A oneshot ordered Before= is the arrangement that actually works.
  sudo tee /usr/local/bin/typesafe-dbenv >/dev/null <<'SCRIPT'
#!/usr/bin/env bash
# Fetches the database password from Secret Manager into a file only root and
# the service account can read. Uses the VM's attached service account, so the
# password is never stored on the disk between boots.
set -euo pipefail

. /etc/typesafe/sql.conf

meta() { curl -fsS -H 'Metadata-Flavor: Google' "http://metadata.google.internal/computeMetadata/v1/$1"; }

TOKEN=$(meta 'instance/service-accounts/default/token' |
  grep -o '"access_token":"[^"]*"' | cut -d'"' -f4)
[ -n "$TOKEN" ] || { echo "no access token from the metadata server" >&2; exit 1; }

PROJECT_ID=$(meta 'project/project-id')
PASSWORD=$(curl -fsS -H "Authorization: Bearer $TOKEN" \
  "https://secretmanager.googleapis.com/v1/projects/$PROJECT_ID/secrets/${DB_SECRET}/versions/latest:access" |
  grep -o '"data":"[^"]*"' | cut -d'"' -f4 | base64 -d)
[ -n "$PASSWORD" ] || { echo "empty password from Secret Manager" >&2; exit 1; }

umask 077
install -d -m 0750 -o root -g typesafe /run/typesafe
# The proxy holds the TLS session to Cloud SQL; this hop is loopback on one host.
printf 'TYPESAFE_DSN=postgres://%s:%s@127.0.0.1:5432/%s?sslmode=disable\n' \
  "$DB_USER" "$PASSWORD" "$DB_NAME" >/run/typesafe/db.env
chown root:typesafe /run/typesafe/db.env
chmod 0640 /run/typesafe/db.env
SCRIPT
  sudo chmod 755 /usr/local/bin/typesafe-dbenv

  sudo tee /etc/systemd/system/typesafe-dbenv.service >/dev/null <<UNIT
[Unit]
Description=Fetch the typesafe database credentials
After=network-online.target
Wants=network-online.target
Before=typesafe.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/bin/typesafe-dbenv

[Install]
WantedBy=multi-user.target
UNIT
}

# Database wiring, if deploy/sql.sh has configured one on this machine.
#
# The configuration lives on the VM rather than travelling with each deploy, so
# a deploy — from a laptop or from CI — never has to know the database exists
# and can never accidentally deconfigure it.
SQL_UNITS=""
SQL_ENVIRONMENT=""
if [ -f /etc/typesafe/sql.conf ]; then
  # shellcheck disable=SC1091
  . /etc/typesafe/sql.conf
  SQL_UNITS="typesafe-dbenv.service typesafe-sqlproxy.service"
  SQL_ENVIRONMENT="EnvironmentFile=/run/typesafe/db.env"
  install_proxy
  write_proxy_unit
  write_dbenv_unit
  echo "database configured: $SQL_CONNECTION_NAME"
fi

# install(1) unlinks the destination before writing, so replacing the running
# executable is atomic and never hits ETXTBSY.
sudo install -m 755 "$SRC" /usr/local/bin/typesafe
rm -f "$SRC"

sudo tee /etc/systemd/system/typesafe.service >/dev/null <<UNIT
[Unit]
Description=typesafe SSH typing server
After=network-online.target ${SQL_UNITS}
Wants=network-online.target
Requires=${SQL_UNITS}

[Service]
Type=exec
User=typesafe
Group=typesafe
ExecStart=/usr/local/bin/typesafe -host 0.0.0.0 -port ${PORT} -host-key /var/lib/typesafe/host_ed25519
${SQL_ENVIRONMENT}
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

# Enable the database units first so Requires= has something to pull in, and
# restart them before the server: a stale proxy pointed at the old instance
# would let typesafe start against the wrong database.
for unit in $SQL_UNITS; do
  sudo systemctl enable "$unit" >/dev/null
  sudo systemctl restart "$unit" || fail "$unit did not start"
done

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
