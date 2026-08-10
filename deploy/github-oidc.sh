#!/usr/bin/env bash
#
# One-time setup so GitHub Actions can deploy to the GCE VM without a
# long-lived key. Creates a deploy service account, a Workload Identity
# Federation pool scoped to this repository, and turns on OS Login for the
# instance. Prints the three repository variables the workflow needs.
#
#   PROJECT=my-project ./deploy/github-oidc.sh
#
# Idempotent: re-running it is a no-op on anything that already exists. Run
# deploy/gcp.sh first — this expects the instance to be there.

set -euo pipefail

PROJECT="${PROJECT:?set PROJECT to the target GCP project id}"
REPO="${REPO:-Pikaryu729/typesafe}"     # owner/name; only this repo may authenticate
INSTANCE="${INSTANCE:-typesafe}"
POOL="${POOL:-github}"
PROVIDER="${PROVIDER:-typesafe}"
SA_NAME="${SA_NAME:-typesafe-deployer}"

SA="${SA_NAME}@${PROJECT}.iam.gserviceaccount.com"

say() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
g() { gcloud --project "$PROJECT" "$@"; }
has() { "$@" >/dev/null 2>&1; }
wif() { g iam workload-identity-pools "$@" --location=global; }

say "APIs"
g services enable iam.googleapis.com iamcredentials.googleapis.com sts.googleapis.com \
  compute.googleapis.com iap.googleapis.com oslogin.googleapis.com

say "Service account: $SA"
if has g iam service-accounts describe "$SA"; then
  echo "already exists"
else
  g iam service-accounts create "$SA_NAME" --display-name "typesafe GitHub Actions deployer" >/dev/null
fi

say "Roles"
# Deliberately narrow. The deployer may find the instance, tunnel to it, and
# log in with sudo — it cannot create, delete or reconfigure infrastructure.
#   compute.viewer            look up the instance and its zone
#   iap.tunnelResourceAccessor  reach port 22 through the IAP tunnel
#   compute.osAdminLogin      log in as a sudoer to install and restart
for role in roles/compute.viewer roles/iap.tunnelResourceAccessor roles/compute.osAdminLogin; do
  g projects add-iam-policy-binding "$PROJECT" \
    --member "serviceAccount:$SA" --role "$role" --condition=None >/dev/null
  echo "$role"
done

say "Workload identity pool"
if has wif describe "$POOL"; then
  echo "pool $POOL already exists"
else
  wif create "$POOL" --display-name "GitHub Actions" >/dev/null
fi

# The attribute condition is the security boundary: without it, any GitHub
# repository in the world could mint a token this provider accepts.
if has wif providers describe "$PROVIDER" --workload-identity-pool="$POOL"; then
  echo "provider $PROVIDER already exists"
else
  wif providers create-oidc "$PROVIDER" \
    --workload-identity-pool="$POOL" \
    --display-name "typesafe repo" \
    --issuer-uri "https://token.actions.githubusercontent.com" \
    --attribute-mapping "google.subject=assertion.sub,attribute.repository=assertion.repository,attribute.repository_owner=assertion.repository_owner,attribute.ref=assertion.ref" \
    --attribute-condition "assertion.repository == '$REPO'" >/dev/null
fi

PROJECT_NUMBER=$(g projects describe "$PROJECT" --format='value(projectNumber)')
MEMBER="principalSet://iam.googleapis.com/projects/$PROJECT_NUMBER/locations/global/workloadIdentityPools/$POOL/attribute.repository/$REPO"

say "Let $REPO impersonate the deployer"
g iam service-accounts add-iam-policy-binding "$SA" \
  --member "$MEMBER" --role roles/iam.workloadIdentityUser --condition=None >/dev/null
echo "$MEMBER"

say "OS Login on $INSTANCE"
# How a service account gets an SSH login at all: without OS Login there is no
# POSIX account to map it to. This also changes how *you* ssh to the box — your
# Google identity now grants the login, which is the better arrangement anyway.
# Guarded: pipefail would otherwise abort the script on a lookup failure, after
# the pool and bindings were created but before the variables are printed.
ZONE=""
if ZONE_FOUND=$(g compute instances list --filter="name=$INSTANCE" --format='value(zone)' 2>/dev/null | head -1); then
  ZONE="$ZONE_FOUND"
fi
if [ -z "$ZONE" ]; then
  echo "WARNING: no instance named $INSTANCE; run deploy/gcp.sh, then re-run this" >&2
else
  g compute instances add-metadata "$INSTANCE" --zone "$ZONE" --metadata enable-oslogin=TRUE >/dev/null
  echo "enabled ($ZONE)"
fi

PROVIDER_RESOURCE=$(wif providers describe "$PROVIDER" \
  --workload-identity-pool="$POOL" --format='value(name)')

say "Set these repository variables"
cat <<OUT

  gh variable set GCP_PROJECT --body '$PROJECT'
  gh variable set GCP_WORKLOAD_IDENTITY_PROVIDER --body '$PROVIDER_RESOURCE'
  gh variable set GCP_DEPLOY_SERVICE_ACCOUNT --body '$SA'

They are variables, not secrets: none of it is confidential, and the pool only
trusts tokens that say they came from $REPO.

Then tag a release to deploy:

  git tag v0.1.0 && git push origin v0.1.0

OUT
