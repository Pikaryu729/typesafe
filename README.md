# typesafe

An SSH-accessible typing-practice and typing-race application. Connect over SSH, get a
terminal UI, practice your typing or race other connected users head-to-head on the same
passage.

## Running

```sh
go run ./cmd/server
```

Then, from another terminal:

```sh
ssh -p 2222 yourname@localhost
```

Any public key is accepted. There is no signup and no password: the key you connected with *is*
your account, and the username you type becomes your display name the first time it is seen.

## What you can do

**Practice** — type a generated 30-word passage at your own pace, with per-character feedback
and live WPM and accuracy. The clock starts on your first keystroke.

**Race** — create a lobby or join one from the browser (or by its four-character code), ready
up, and race everyone else on the same passage. You see opponents' progress bars move in real
time. Everyone is timed from the same instant, so hesitating at the start costs you. The host
can call a rematch on a fresh passage.

**Profile** — personal bests, lifetime totals, a sparkline of recent speed and a list of your
last runs. Both practice and races are recorded.

**Shop** — spend the bytes you have earned on cosmetics: a colour for your name, a title beside
it, the glyphs your race bar is drawn with, and a colour scheme for your own passage. The first
three are what everyone else in the lobby sees.

**Link a device** — a second machine has a different SSH key, so it starts out as a different
typist. Press `c` on the machine you are known on, type the code on the new one, and the two
accounts merge, saved history and cosmetics included.

Keys are shown at the bottom of every screen. `ctrl+c` disconnects from anywhere.

## Bytes

Finishing a passage pays **bytes**, and bytes buy cosmetics. There is one currency, typing is
its only source, and there is no code path anywhere that exchanges money for it — nothing here
is gated, nothing expires, and none of it touches how a race is scored or won.

A race pays a small random base plus three bonuses you can see itemised the moment it ends:

```
  you placed 1st

  base             +11
  win bonus        +15
  accuracy bonus    +8
  speed bonus       +5
  ────────────────────
  total            +39 bytes

  balance          287 bytes
```

Only the base is random. The rest is deterministic, so the breakdown is something you can learn
from: the placement bonus pays for each finisher you actually beat, which is why winning a full
lobby is worth well over winning a duel, while idle sessions add nothing. A race you do not finish
pays nothing at all.

Practice pays a trickle on the same rules, minus the placement — enough that being alone on the
server still gets you somewhere, little enough that racing is obviously the point.

Cosmetics run from 120 bytes for a race bar to 500 for a rare name colour, so the first one is
four or five races away. Buying something wears it; pressing enter on it again puts it away.
Nothing is ever lost: prices are recorded as paid, so the catalogue can be repriced without
touching anyone's balance.

Your balance is not stored anywhere. It is computed as everything you have earned less
everything you have bought, which is why it cannot drift out of step with the history behind
it — and why merging two accounts refunds any cosmetic they both owned.

## Accounts and history

Practice and racing work without a database; bytes and cosmetics, like the profile, need
somewhere to keep them. Passing `-dsn` (or setting `TYPESAFE_DSN`) to a PostgreSQL connection
string turns on accounts, run history, the profile screen and the shop:

```sh
go run ./cmd/server -dsn 'postgres://typesafe:typesafe@localhost:5432/typesafe?sslmode=disable'
```

The schema is created and migrated on startup; there is nothing to run by hand.

**Without a DSN the server still works.** Practice and racing behave exactly as they always did,
sessions are anonymous, no bytes are earned or promised, and the account-dependent menu items
are shown greyed out rather than hidden, so the reason is visible. The database is never on the
typing path: writes are queued and dropped rather than allowed to block a keystroke, and reads
happen off the update loop — a race's bytes ride in the same row as the race, so finishing one
still costs a single queued statement. A database that falls over mid-race costs you the record
of that race, and the bytes it would have paid, and nothing else.

A DSN that is set but unreachable *at startup* is a hard failure, deliberately — otherwise a
typo yields a server that passes every health check while quietly recording nothing.

## Development

```sh
make build    # build the server binary
make run      # run the server
make test     # go test ./... -race
make lint     # gofmt check + go vet
```

The default test run needs no database. The Postgres repository has its own tests behind a build
tag, which CI runs against a service container:

```sh
TYPESAFE_TEST_DSN='postgres://...' go test -tags integration ./internal/store/...
```

CI runs the same targets on every pull request, plus `govulncheck` and a cross-compile; see
[Continuous integration and deployment](#continuous-integration-and-deployment).

## Deployment

typesafe deploys as a **single static binary** with no config file and no runtime dependencies.
The only state on the machine itself is the SSH host key; lobbies and in-flight races live in
memory and are gone on restart.

A database is optional. Without one you get the deployment described below and no accounts;
with one you additionally get history, and the details are under
[Accounts on Cloud SQL](#accounts-on-cloud-sql).

The walkthrough below targets a Linux host with systemd. It assumes you are deploying to
`example.com` and running the service as a dedicated unprivileged user.

### 1. Build

Build on any machine with Go 1.26+; the result is a static binary you can copy to a server that
has no Go toolchain:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags="-s -w" -o typesafe ./cmd/server
```

`CGO_ENABLED=0` is what makes it static, so it runs on any glibc/musl distribution regardless of
version. Use `GOARCH=arm64` for ARM servers. The binary is about 6 MB.

Confirm before shipping it:

```sh
file typesafe    # ...ELF 64-bit LSB executable, statically linked, stripped
```

### 2. Install on the server

```sh
# Copy the binary up
scp typesafe root@example.com:/usr/local/bin/typesafe
ssh root@example.com 'chmod 755 /usr/local/bin/typesafe'

# A dedicated user with no shell and no home
ssh root@example.com 'useradd --system --no-create-home --shell /usr/sbin/nologin typesafe'
```

There is no directory to create by hand: the systemd unit below uses `StateDirectory=`, which
makes `/var/lib/typesafe` on start, owned by the service user, mode `0700`.

### 3. The host key

**This is the part worth getting right.** The server generates an ed25519 host key on first
start if one is not already there, and reuses it afterwards. Its path must be **absolute and on
persistent storage**.

The default is `.ssh/typesafe_ed25519`, *relative to the working directory* — fine for local
development, wrong for a service. If the key ends up somewhere ephemeral it is regenerated on
every restart, and every returning user is met with `WARNING: REMOTE HOST IDENTIFICATION HAS
CHANGED` and refused a connection until they edit `known_hosts`. Point `-host-key` at
`/var/lib/typesafe/host_ed25519`, as the unit below does.

Missing parent directories are created for you, with the key written `0600`.

To keep an existing identity — reinstalling a host, or moving between machines — copy both
`host_ed25519` and `host_ed25519.pub` across before first start, preserving ownership and mode.
Record the fingerprint so you can tell users what to expect:

```sh
ssh-keygen -lf /var/lib/typesafe/host_ed25519.pub
```

### 4. The systemd unit

Write `/etc/systemd/system/typesafe.service`:

```ini
[Unit]
Description=typesafe SSH typing server
After=network-online.target
Wants=network-online.target

[Service]
Type=exec
User=typesafe
Group=typesafe
ExecStart=/usr/local/bin/typesafe -host 0.0.0.0 -port 2222 -host-key /var/lib/typesafe/host_ed25519

# Creates /var/lib/typesafe, owned by the service user
StateDirectory=typesafe
StateDirectoryMode=0700

Restart=on-failure
RestartSec=2s
# The server drains live sessions for up to 10s on SIGTERM; leave it room
KillSignal=SIGTERM
TimeoutStopSec=20s

# Hardening. The service needs exactly one thing from the host: a TCP socket.
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
```

Check it before enabling — this catches typos that would otherwise surface as a failed start:

```sh
systemd-analyze verify /etc/systemd/system/typesafe.service
```

Then:

```sh
systemctl daemon-reload
systemctl enable --now typesafe
```

### 5. Verify

```sh
systemctl status typesafe          # active (running)
journalctl -u typesafe -f          # "INFO typesafe listening addr=0.0.0.0:2222"
```

From your own machine, connect for real:

```sh
ssh -p 2222 yourname@example.com
```

You should land on the main menu. Sanity-check the whole path: run a practice attempt, then open
a second terminal, join the first player's lobby by code, and race. If the second player never
appears in the first player's lobby, the two sessions are not sharing state — check you are not
somehow running two processes.

### 6. Firewall

Open the port you chose:

```sh
ufw allow 2222/tcp                              # ufw
firewall-cmd --permanent --add-port=2222/tcp    # firewalld
firewall-cmd --reload
```

If the host is behind a cloud security group, open it there too.

### Running on port 22

Port 2222 avoids a fight with the system's own `sshd`, which almost always owns 22. If you want
users to type plain `ssh you@example.com`, you have three options, in rough order of sanity:

1. **Move your admin sshd to another port** (say 2022), then give typesafe port 22. Do this
   carefully and keep an existing session open while you test the new one — locking yourself out
   of a remote box is easy here.
2. **Bind each to a different address**, if the host has more than one IP: point typesafe at one
   with `-host 203.0.113.10` and restrict `sshd` to the other with `ListenAddress`.
3. **Leave it on 2222** and tell people the port.

Binding below 1024 as an unprivileged user needs one capability. Rather than editing the unit,
add a drop-in with `systemctl edit typesafe`, which writes
`/etc/systemd/system/typesafe.service.d/override.conf`:

```
[Service]
ExecStart=
ExecStart=/usr/local/bin/typesafe -host 0.0.0.0 -port 22 -host-key /var/lib/typesafe/host_ed25519
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
```

Two things there are easy to get wrong. The empty `ExecStart=` is required: without it systemd
appends a second command rather than replacing the first, and refuses to start a `Type=exec`
service with two. And the capability lines must *override* rather than add, because the unit
above deliberately sets both to empty.

Then `systemctl daemon-reload && systemctl restart typesafe`.

### Upgrading

A restart disconnects everyone and destroys every lobby and in-flight race — see the limits
below. Deploy when nobody is mid-race, or accept it.

```sh
scp typesafe root@example.com:/usr/local/bin/typesafe.new
ssh root@example.com '
  install -m 755 /usr/local/bin/typesafe.new /usr/local/bin/typesafe &&
  rm /usr/local/bin/typesafe.new &&
  systemctl restart typesafe &&
  systemctl is-active typesafe'
```

Replacing the file before restarting keeps the swap atomic, so a partially copied binary is
never the one systemd runs. The host key is untouched, so returning users see no warning.

To roll back, put the previous binary back and restart. Migrations only ever run forward, so an
older binary against a newer schema is the case to think about before rolling back across a
release that added one — the columns it does not know about are simply unused, which is fine for
additive changes and not for anything else.

On the Google Cloud path this is automated — push a `v*` tag and GitHub Actions does it, keeping
the outgoing binary for the rollback. See
[Continuous integration and deployment](#continuous-integration-and-deployment).

### Logs and monitoring

Logs go to stdout/stderr and therefore to the journal. Each connection logs on open and close
with its duration:

```sh
journalctl -u typesafe -f
journalctl -u typesafe --since "1 hour ago" | grep connect
```

There are no metrics endpoints and no health check. `systemctl is-active typesafe` plus a TCP
check on the port is the whole story; an external monitor can just open a socket.

Sizing, measured on this build: about 6.7 MB resident when idle, and roughly 400 KB per
connected session (8 concurrent sessions took it to about 10 MB). Resident memory does not
shrink immediately when users leave — the Go runtime holds freed memory for reuse. A small VPS
handles a lot of typists.

### Operational limits

Known and deliberate for v1. Worth understanding before putting this somewhere public.

- **Live state is in memory.** Restarting drops every session, lobby and running race. Finished
  runs are in the database and survive; a race interrupted mid-passage is simply gone.
- **A single process cannot be scaled horizontally.** Lobbies live in one process's memory, so
  two instances behind a load balancer would not share them — players would be unable to see
  each other depending on which they landed on. Run exactly one. The database does not change
  this: it holds history, not live lobbies.
- **Any public key is accepted.** This is the design: the key identifies you, it does not
  authorise you. Anyone who can reach the port can use the server and will get an account by
  doing so, and two people can still connect under the same display name.
- **A link code hands over an account.** It is short-lived and single-use, but anyone who reads
  one before it is redeemed gets the account it belongs to. It is closer to a password than to a
  lobby code, and the screen says so.
- **No idle timeout.** A session left open holds its connection indefinitely. `wish` supports
  `WithIdleTimeout`, but the server does not currently set one.
- **No rate limiting or per-IP connection cap.** `wish` ships a rate-limiter middleware that is
  not wired up.

The last two matter mainly if you expose this to the open internet. Restricting the port to a
VPN or a known set of addresses sidesteps both.

### Google Cloud (Compute Engine)

Everything above applies unchanged on a GCE VM — the build, the service user, the systemd unit.
This section covers only what is specific to Google Cloud. It has been run end to end.

**Use Compute Engine, not Cloud Run.** typesafe is an SSH server on a raw TCP port. Cloud Run
serves HTTP, gRPC and WebSockets only, with no raw TCP ingress, so it cannot host this at all —
the same goes for App Engine. GKE would work via a TCP `LoadBalancer` service, but a Kubernetes
cluster to run one 6 MB single-process binary is not a trade worth making. A single small VM is
the right shape, and it has to be a *single* VM: all state is in memory and lobbies live in one
process, so two instances behind a load balancer would silently hide players from each other.

An `e2-micro` is plenty. Measured: ~6.7 MB resident idle, ~400 KB per session.

#### One command

```sh
PROJECT=your-project-id BILLING=0X0X0X-0X0X0X-0X0X0X ./deploy/gcp.sh
```

It creates the project if needed, enables the APIs, sets up the firewall, creates the VM,
promotes its address to a static one, builds and uploads the binary, installs the systemd unit,
and backs up the host key. Re-running it rebuilds and restarts the service in place — that is
the upgrade path, and it is safe to run against a live deployment.

Useful variables: `REGION`, `ZONES`, `MACHINE`, `PORT`, `INSTANCE`, and `SOURCE_RANGE`
(who may connect — defaults to your current public IP only).

#### The four Google-specific traps

**Do not give typesafe port 22.** On GCE that is how you administer the box. The "Running on
port 22" section above is explicitly *not* for GCE: taking 22 means fighting the VM's own
`sshd`, and locking yourself out of a cloud VM is a bad afternoon. Stay on 2222.

**The default VPC blocks your port.** A fresh project allows only `22`, `3389` and ICMP
inbound. Without an explicit rule the server looks completely dead from outside while running
perfectly on the VM — the single most likely reason a first deploy appears broken. The script
adds a rule scoped by network tag, and narrows the default `tcp:22` rule from `0.0.0.0/0` to
IAP's range (`35.235.240.0/20`) so admin SSH is not exposed. Administer the box with
`gcloud compute ssh <instance> --tunnel-through-iap`.

**`e2-micro` capacity runs out.** It is the free-tier shape and therefore contended: during this
deployment *all four* `us-central1` zones refused with "does not have enough resources
available". The script walks a list of zones rather than failing on the first. If a whole region
is exhausted, set `REGION`/`ZONES` to another (`us-east1`, `us-west1` and `us-central1` are the
free-tier regions).

**A rebuilt VM loses its host key**, which lives on the boot disk under `/var/lib/typesafe`.
Every returning user would then hit `REMOTE HOST IDENTIFICATION HAS CHANGED` and be refused.
The script copies the key into Secret Manager on first deploy. To restore it onto a fresh VM:

```sh
gcloud secrets versions access latest --secret=typesafe-host-key \
  | gcloud compute ssh <instance> --tunnel-through-iap \
      --command 'sudo install -m 600 -o typesafe -g typesafe /dev/stdin /var/lib/typesafe/host_ed25519'
gcloud compute ssh <instance> --tunnel-through-iap --command 'sudo systemctl restart typesafe'
```

Publish your fingerprint (`ssh-keygen -lf`) so users can check what they should be trusting.

#### Verifying a deployment

Beyond connecting once, three checks are worth doing because each has a distinct failure mode:

1. **A two-player race**, two terminals against the external IP. Exercises the shared store, the
   event pump and the synchronised countdown across genuinely separate connections — none of
   which a single session touches.
2. **Stop and start the VM**, then reconnect. The IP must be unchanged (static address) and the
   fingerprint must be unchanged (persistent disk). No `known_hosts` warning is the pass mark,
   and it also confirms `systemctl enable` survived the reboot.
3. **Prove the firewall restricts by source**, rather than assuming. From an allowed address the
   port should open; from anywhere else — the VM itself is a convenient second source — it
   should be refused.

#### Cost

`e2-micro` in a free-tier region with a 20 GB standard disk sits inside the always-free
allowance, and a static IP is free while attached to a running instance. Two things to watch: a
reserved address is billed when **not** attached, so release it if you delete the VM, and free
tier covers one such instance per month across your whole billing account. Confirm current terms
against Google's pricing rather than taking them from here.

To tear the whole thing down, delete the project — that removes the VM, address, firewall rules
and secret in one go.

### Accounts on Cloud SQL

Optional, and separate from everything above: the server runs perfectly well without it. One
script, after `deploy/gcp.sh` has built the VM:

```sh
PROJECT=your-project-id ./deploy/sql.sh
```

It creates a shared-core Postgres instance, a database and application user, puts the generated
password straight into Secret Manager, creates a `typesafe-vm` service account that may reach
Cloud SQL and read that one secret, attaches it to the VM, and writes `/etc/typesafe/sql.conf`.
Then deploy as usual — the proxy and the wiring install themselves.

Five things worth knowing.

**The instance has a public address, and that is fine.** Cloud SQL refuses an instance with no
connectivity at all, and private IP would mean VPC peering for the sake of one VM. What keeps it
shut is the pair the script sets instead: no authorized networks, so no address on the internet
may connect, and `TRUSTED_CLIENT_CERTIFICATE_REQUIRED`, which only the Auth Proxy satisfies. The
proxy is authorised by IAM rather than by where it connects from.

**Attaching the service account stops the VM.** The instance was created `--no-service-account`,
and `set-service-account` refuses to run against a live one, so there is no way to avoid a
restart. The script warns before doing it. Nothing about the server's identity changes: the host
key is on the boot disk and the address is reserved, so returning users see no `known_hosts`
warning.

**Configuration lives on the VM, not in the deploy.** `sql.conf` is what `remote-install.sh`
reads to decide whether to install the proxy. That means a deploy — from your laptop or from a
tag — never carries database configuration and cannot accidentally remove it.

**The password is never on disk in the repository or the image.** A oneshot unit fetches it from
Secret Manager into `/run/typesafe/db.env` at boot, readable only by root and the service
account. It has to be a separate unit rather than an `ExecStartPre`, because systemd reads
`EnvironmentFile` before a service's own `ExecStartPre` runs.

**Cost.** A `db-f1-micro` instance is outside the always-free tier and is the first thing in this
project that costs money every month — roughly $10 depending on region and storage, which the
$300 trial credit covers for its 90 days. Confirm against Google's pricing rather than this
paragraph. To stop paying: `gcloud sql instances delete typesafe-db`, remove
`/etc/typesafe/sql.conf`, and redeploy; the server drops back to anonymous mode.

### Continuous integration and deployment

Two workflows under `.github/workflows/`.

**`ci.yml`** runs on every push to `main` and every pull request: `gofmt`/`go vet`, `go mod
tidy` with a diff check, the test suite with `-race` and a coverage summary on the job page, a
cross-compile of `linux/amd64` and `linux/arm64` with the same flags a deploy uses, and
`govulncheck`.

**`deploy.yml`** ships to the GCE VM. It runs on a `v*` tag or a manual dispatch — **not** on
merges to `main`, because a deploy restarts the service and that drops every connected session
and running race. It calls `ci.yml` first (a tag can point at a commit CI never ran on), then
builds, uploads through the IAP tunnel, and pipes `deploy/remote-install.sh` over SSH — the same
script `deploy/gcp.sh` uses, so the manual and automated paths install one systemd unit rather
than two that drift.

#### Setting it up

`deploy/gcp.sh` provisions the infrastructure; this only grants GitHub access to it.

```sh
PROJECT=your-project-id ./deploy/github-oidc.sh
```

It creates a `typesafe-deployer` service account, sets up Workload Identity Federation, turns on
OS Login for the instance, and prints three `gh variable set` commands to run. Then:

```sh
git tag v0.1.0 && git push origin v0.1.0
```

**No key is stored in GitHub.** The workflow presents its OIDC token, and the pool trades it for
short-lived Google credentials. Which is the point of the attribute condition the script sets —
the provider accepts tokens only from this repository. Without it, any repository on GitHub
could authenticate as your deployer.

The service account gets three roles and no more: find the instance (`compute.viewer`), reach
port 22 through the tunnel (`iap.tunnelResourceAccessor`), and log in as a sudoer to install and
restart (`compute.osAdminLogin`). It cannot create or destroy anything.

Enabling OS Login changes how *you* reach the box too — your Google identity now grants the
login instead of a key in project metadata. `gcloud compute ssh` keeps working unchanged.

The `production` environment in the workflow is a hook: add required reviewers to it in repo
settings and every deploy waits for an approval.

#### What a deploy checks, and what it can't

`remote-install.sh` fails the job if `systemd-analyze verify` rejects the unit, if the service
is not active after the restart, or if nothing is listening on the port a few seconds later.

The outgoing binary is set aside as `/var/backups/typesafe.pending` and only promoted to
`/var/backups/typesafe.prev` once those checks pass. That ordering is the point: a failed deploy
leaves the last version *known to have come up* as the rollback target, rather than overwriting
it with the broken build it just replaced. The job summary prints the one-line rollback command.

The port comes from the `allow-typesafe` firewall rule unless `TYPESAFE_PORT` says otherwise,
and the job fails if the two disagree. Trusting a default here would rewrite the unit onto a
port the firewall does not admit — and the listen check, probing that same wrong port, would
pass while every user was locked out.

The deploy cannot check the port from outside: the firewall only admits `SOURCE_RANGE`, and a
GitHub runner's address is not in it. That is why the listen check runs on the VM. Connecting
once yourself after a release is still worth it.

If the VM was rebuilt, restore the host key from Secret Manager (above) **before** deploying,
otherwise the new binary comes up with a fresh identity and every returning user hits the
`known_hosts` warning.

### Containers

Not verified in this repository — the systemd path above is the one that has actually been
tested end to end. If you would rather run it in a container, the binary is static, so the image
is trivial:

```dockerfile
FROM scratch
COPY typesafe /typesafe
EXPOSE 2222
ENTRYPOINT ["/typesafe", "-host", "0.0.0.0", "-port", "2222", \
            "-host-key", "/data/host_ed25519"]
```

Mount a volume at `/data` so the host key survives the container being recreated — otherwise
every deploy hands users a new host identity and the `known_hosts` warning that comes with it.
Make sure the container is stopped with `SIGTERM` and given at least 15s to drain
(`docker stop --time 15`).
