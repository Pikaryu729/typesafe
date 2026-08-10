#!/usr/bin/env bash
#
# Give the typesafe VM a Cloud SQL database.
#
#   PROJECT=my-project ./deploy/sql.sh
#
# Idempotent: the first run provisions, later runs reconcile. Run deploy/gcp.sh
# first — this expects the instance to exist.
#
# What it does, and the order matters:
#   1. a Postgres instance, database and application user
#   2. the user's password into Secret Manager, never onto disk here
#   3. a service account for the VM, able to reach Cloud SQL and read that one
#      secret, and nothing else
#   4. attaches it to the VM, which requires stopping the VM
#   5. leaves /etc/typesafe/sql.conf behind, which is how deploy/remote-install.sh
#      knows to install the proxy on every deploy from then on
#
# Afterwards, deploy as usual: ./deploy/gcp.sh, or push a v* tag.

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

PROJECT="${PROJECT:?set PROJECT to the target GCP project id}"
REGION="${REGION:-us-east1}"
INSTANCE="${INSTANCE:-typesafe}"          # the VM
SQL_INSTANCE="${SQL_INSTANCE:-typesafe-db}"
TIER="${TIER:-db-f1-micro}"               # smallest shared-core shape
DB_VERSION="${DB_VERSION:-POSTGRES_17}"
DB_NAME="${DB_NAME:-typesafe}"
DB_USER="${DB_USER:-typesafe}"
DB_SECRET="${DB_SECRET:-typesafe-db-password}"
SA_NAME="${SA_NAME:-typesafe-vm}"
STORAGE="${STORAGE:-10GB}"
BACKUP_START="${BACKUP_START:-03:00}"

SA="${SA_NAME}@${PROJECT}.iam.gserviceaccount.com"

say() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
g() { gcloud --project "$PROJECT" "$@"; }
has() { "$@" >/dev/null 2>&1; }

# retry rides out eventual consistency. A freshly created service account is
# not immediately visible to other services, so granting it a role seconds
# later fails with "does not exist" — which is a timing problem, not a real
# one, and should not abandon a half-finished provisioning run.
retry() {
  local n=1
  until "$@"; do
    if [ "$n" -ge 6 ]; then
      echo "giving up after $n attempts: $*" >&2
      return 1
    fi
    echo "  retrying in 10s (attempt $n)"
    n=$((n + 1))
    sleep 10
  done
}

say "APIs"
g services enable sqladmin.googleapis.com secretmanager.googleapis.com

say "Cloud SQL instance: $SQL_INSTANCE ($TIER, $DB_VERSION)"
if has g sql instances describe "$SQL_INSTANCE"; then
  echo "already exists"
else
  # An address is unavoidable — Cloud SQL refuses an instance with no
  # connectivity at all, and private IP would mean setting up VPC peering for
  # one VM. What actually keeps it shut is the pair below: no authorized
  # networks, so no address on the internet may connect, and a client
  # certificate required, which only the Auth Proxy has. The proxy is
  # authorised by IAM rather than by where it is connecting from.
  g sql instances create "$SQL_INSTANCE" \
    --database-version "$DB_VERSION" \
    --tier "$TIER" \
    --edition ENTERPRISE \
    --region "$REGION" \
    --storage-size "$STORAGE" \
    --storage-auto-increase \
    --availability-type zonal \
    --backup --backup-start-time "$BACKUP_START" \
    --ssl-mode TRUSTED_CLIENT_CERTIFICATE_REQUIRED >/dev/null
  # Authorized networks are deliberately not set: none is the default, and none
  # is what we want.
  echo "created"
fi

CONNECTION_NAME=$(g sql instances describe "$SQL_INSTANCE" --format='value(connectionName)')
echo "connection name: $CONNECTION_NAME"

say "Database and user"
has g sql databases describe "$DB_NAME" --instance "$SQL_INSTANCE" ||
  g sql databases create "$DB_NAME" --instance "$SQL_INSTANCE" >/dev/null

# The password is generated here, goes straight into Secret Manager, and is
# never written to disk or echoed. Rotating it means re-running with the
# secret deleted.
if has g secrets describe "$DB_SECRET"; then
  echo "password already in Secret Manager"
else
  umask 077
  PASSWORD=$(openssl rand -base64 32 | tr -d '\n/+=' | head -c 32)
  printf '%s' "$PASSWORD" | g secrets create "$DB_SECRET" --data-file=- --replication-policy=automatic >/dev/null
  echo "password stored as $DB_SECRET"
fi

# Set the user's password from the secret every run, so the database and the
# secret cannot drift apart.
PASSWORD=$(g secrets versions access latest --secret="$DB_SECRET")
if has g sql users describe "$DB_USER" --instance "$SQL_INSTANCE"; then
  g sql users set-password "$DB_USER" --instance "$SQL_INSTANCE" --password "$PASSWORD" >/dev/null
else
  g sql users create "$DB_USER" --instance "$SQL_INSTANCE" --password "$PASSWORD" >/dev/null
fi
unset PASSWORD
echo "user $DB_USER ready"

say "Service account for the VM: $SA"
has g iam service-accounts describe "$SA" ||
  g iam service-accounts create "$SA_NAME" --display-name "typesafe VM" >/dev/null

# Narrow on purpose: connect to Cloud SQL, and read one secret. Not project
# viewer, not editor.
retry g projects add-iam-policy-binding "$PROJECT" \
  --member "serviceAccount:$SA" --role roles/cloudsql.client --condition=None >/dev/null
retry g secrets add-iam-policy-binding "$DB_SECRET" \
  --member "serviceAccount:$SA" --role roles/secretmanager.secretAccessor >/dev/null
echo "roles/cloudsql.client + secretAccessor on $DB_SECRET"

say "Attach it to the VM"
ZONE=""
if ZONE_FOUND=$(g compute instances list --filter="name=$INSTANCE" --format='value(zone)' 2>/dev/null | head -1); then
  ZONE="$ZONE_FOUND"
fi
[ -n "$ZONE" ] || { echo "no instance named $INSTANCE; run deploy/gcp.sh first" >&2; exit 1; }

CURRENT_SA=$(g compute instances describe "$INSTANCE" --zone "$ZONE" --format='value(serviceAccounts[0].email)')
if [ "$CURRENT_SA" = "$SA" ]; then
  echo "already attached"
else
  # This is the one step with downtime. set-service-account refuses to run
  # against a live instance, and the VM was created --no-service-account, so
  # there is no way round it. The host key is on the boot disk and the address
  # is reserved, so nothing about the server's identity changes.
  echo "stopping $INSTANCE — connected users will be disconnected"
  g compute instances stop "$INSTANCE" --zone "$ZONE" >/dev/null
  g compute instances set-service-account "$INSTANCE" --zone "$ZONE" \
    --service-account "$SA" \
    --scopes https://www.googleapis.com/auth/cloud-platform >/dev/null
  g compute instances start "$INSTANCE" --zone "$ZONE" >/dev/null
  echo "restarted with $SA"
fi

say "Tell the VM about the database"
# remote-install.sh reads this on every deploy and installs the proxy from it,
# so deploys do not have to carry database configuration.
#
# Written to a temp file first because this may have to be retried: the VM was
# very likely just restarted above, and sshd takes a while to start accepting
# connections after a boot.
CONF_TMP=$(mktemp)
trap 'rm -f "$CONF_TMP"' EXIT
cat >"$CONF_TMP" <<CONF
# Written by deploy/sql.sh. Read by deploy/remote-install.sh.
SQL_CONNECTION_NAME=$CONNECTION_NAME
DB_SECRET=$DB_SECRET
DB_USER=$DB_USER
DB_NAME=$DB_NAME
CONF

write_conf() {
  g compute ssh "$INSTANCE" --zone "$ZONE" --tunnel-through-iap --quiet \
    --command "sudo install -d -m 0755 /etc/typesafe && sudo tee /etc/typesafe/sql.conf >/dev/null" \
    <"$CONF_TMP" >/dev/null 2>&1
}
retry write_conf
echo "wrote /etc/typesafe/sql.conf"

say "Done"
cat <<OUT

Deploy to pick it up:

  PROJECT=$PROJECT $HERE/gcp.sh

The proxy and the database wiring install themselves from sql.conf. Check it
came up with:

  gcloud compute ssh $INSTANCE --project $PROJECT --zone $ZONE --tunnel-through-iap \\
    --command 'systemctl is-active typesafe typesafe-sqlproxy && journalctl -u typesafe -n 20 --no-pager'

OUT
