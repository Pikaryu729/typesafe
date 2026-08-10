#!/usr/bin/env bash
#
# Deploy typesafe to a single Google Compute Engine VM.
#
# Idempotent: the first run provisions everything, and every run after that
# rebuilds the binary and restarts the service in place. Re-running it is the
# upgrade path.
#
#   PROJECT=my-project BILLING=0X0X0X-0X0X0X-0X0X0X ./deploy/gcp.sh
#
# Everything else has a default. See the Google Cloud section of the README for
# what this does and why.

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

PROJECT="${PROJECT:?set PROJECT to the target GCP project id}"
BILLING="${BILLING:-}"                 # only needed the first time
REGION="${REGION:-us-east1}"
ZONES="${ZONES:-us-east1-b us-east1-c us-east1-d}"
MACHINE="${MACHINE:-e2-micro}"
INSTANCE="${INSTANCE:-typesafe}"
PORT="${PORT:-2222}"
DISK_SIZE="${DISK_SIZE:-20GB}"
# Who may reach the typing server. Defaults to just this machine.
SOURCE_RANGE="${SOURCE_RANGE:-$(curl -fsS https://ifconfig.me)/32}"

say() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
g() { gcloud --project "$PROJECT" "$@"; }
# Does a resource exist? Runs a describe and throws away the noise.
has() { "$@" >/dev/null 2>&1; }

say "Project and APIs"
if ! has gcloud projects describe "$PROJECT"; then
  gcloud projects create "$PROJECT" --name=typesafe
fi
if [ -n "$BILLING" ]; then
  gcloud billing projects link "$PROJECT" --billing-account="$BILLING" >/dev/null
fi
# Enabling an already-enabled service is a no-op, so this is safe to repeat.
g services enable compute.googleapis.com secretmanager.googleapis.com iap.googleapis.com

say "Firewall: tcp:$PORT from $SOURCE_RANGE"
if has g compute firewall-rules describe allow-typesafe; then
  g compute firewall-rules update allow-typesafe \
    --allow "tcp:$PORT" --source-ranges "$SOURCE_RANGE" >/dev/null
else
  g compute firewall-rules create allow-typesafe \
    --allow "tcp:$PORT" --source-ranges "$SOURCE_RANGE" --target-tags typesafe \
    --description "typesafe SSH typing server" >/dev/null
fi

# Admin SSH is open to the whole internet in a fresh project. Narrow it to the
# range IAP tunnels arrive from, so port 22 is not exposed.
if has g compute firewall-rules describe default-allow-ssh; then
  g compute firewall-rules update default-allow-ssh --source-ranges 35.235.240.0/20 >/dev/null
fi

say "Instance"
ZONE=""
if ZONE_FOUND=$(g compute instances list --filter="name=$INSTANCE" --format='value(zone)' | head -1) \
   && [ -n "$ZONE_FOUND" ]; then
  ZONE="$ZONE_FOUND"
  echo "already exists in $ZONE"
else
  # e2-micro is the free-tier shape and is often exhausted in a given zone, so
  # fall through the list rather than failing on the first one.
  for z in $ZONES; do
    echo "trying $z..."
    if g compute instances create "$INSTANCE" \
        --zone "$z" --machine-type "$MACHINE" \
        --image-family debian-12 --image-project debian-cloud \
        --boot-disk-size "$DISK_SIZE" --boot-disk-type pd-standard \
        --tags typesafe --no-service-account --no-scopes \
        --shielded-secure-boot >/dev/null 2>&1; then
      ZONE="$z"; echo "created in $z"; break
    fi
  done
  [ -n "$ZONE" ] || { echo "no zone in '$ZONES' had capacity for $MACHINE" >&2; exit 1; }
fi

say "Static IP"
IP=$(g compute instances describe "$INSTANCE" --zone "$ZONE" \
  --format='value(networkInterfaces[0].accessConfigs[0].natIP)')
# Promote the address the instance already has, rather than reserving one up
# front and discovering the zone has no capacity.
if ! has g compute addresses describe typesafe-ip --region "$REGION"; then
  g compute addresses create typesafe-ip --region "$REGION" --addresses="$IP" >/dev/null
fi
echo "$IP"

say "Build"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags="-s -w" -o /tmp/typesafe-deploy ./cmd/server

say "Upload and install"
# Upload into the login user's home, not /tmp: CI deploys log in as a different
# user and /tmp is sticky, so a shared path would leave each unable to replace
# the other's file.
g compute scp /tmp/typesafe-deploy "$INSTANCE:typesafe.new" --zone "$ZONE" --tunnel-through-iap >/dev/null
g compute ssh "$INSTANCE" --zone "$ZONE" --tunnel-through-iap --command "PORT=$PORT bash -s" < "$HERE/remote-install.sh"

say "Back up the host key"
# Without this, rebuilding the VM hands every returning user a changed host
# identity and the known_hosts warning that follows it.
if has g secrets describe typesafe-host-key; then
  echo "already backed up"
else
  umask 077
  g compute ssh "$INSTANCE" --zone "$ZONE" --tunnel-through-iap \
    --command 'sudo cat /var/lib/typesafe/host_ed25519' 2>/dev/null > /tmp/ts-hostkey
  if head -1 /tmp/ts-hostkey | grep -q 'BEGIN OPENSSH PRIVATE KEY'; then
    g secrets create typesafe-host-key --data-file=/tmp/ts-hostkey --replication-policy=automatic >/dev/null
    echo "stored in Secret Manager"
  else
    echo "WARNING: could not read a valid host key; skipping backup" >&2
  fi
  shred -u /tmp/ts-hostkey 2>/dev/null || rm -f /tmp/ts-hostkey
fi

say "Done"
echo "  ssh -p $PORT yourname@$IP"
