# Auth & deployment roadmap

> **Status:** working design, not yet implemented. This doc is where we work
> out the auth and multi-user story before the code lands.

The features described here are listed in [`design.md`](design.md) as Phase 11
and Phase 12. They are large enough — and connected enough — that they get
their own doc to iterate on. Once they ship, this becomes the operator's
guide; until then, treat it as a proposal.

A note on jargon: the topic touches network, OS, and HTTP concepts that are
not all uniformly familiar. Each piece of vocabulary is defined the first time
it appears.

**Phase ordering note.** An earlier draft of this roadmap put deployment-shape
work (TLS, bind address, Docker images, single-tenant auth) as Phase 11 and
multi-user safety as Phase 12. We have since flipped that: multi-user safety
is the more important blocker for the on-prem PWB deployment, and the two
phases are technically independent (see §7). What was previously called
"Phase 12 (auth/multi-user)" is now **Phase 11**. What was previously called
"Phase 11 (deploy & secure)" is now **Phase 12**.

---

## 1. Where daggle is today

daggle's **defaults** are still the on-prem PWB sidecar, and that is still the
only shape with an end-to-end operational story.
([§9](#9-deployment-profile-managed-multi-user-r-hosts) describes the
deployment profile that shape lands in, and what the alternative shape there
would demand.) In that shape:

- `daggle serve` runs as a `supervisord` process inside a Posit Workbench (PWB)
  container.
- The HTTP API binds to **127.0.0.1** (the loopback address — only callers on
  the same machine can reach it). The port is never exposed outside the
  container.
- There is **no authentication**. Every R session inside the container can
  call any API endpoint as an anonymous, equally-privileged caller.

Those last two are now *defaults* rather than the only option: the first part
of Phase 12 has shipped, so a deployment can bind elsewhere and turn on
single-tenant auth (§4). What has **not** changed is everything below — the
daemon is still root and steps still inherit its identity, which is why
Phase 11 remains the more important piece of work.
- The daemon runs as **`root`** so it can read every user's home directory to
  discover `.daggle/` project folders.
- DAG steps inherit the daemon's identity. **Every step runs as root.** A
  scheduled DAG submitted by user A executes with root privileges over user
  B's files.

This is acceptable for trusted teams in a contained network: the threat model
assumes anyone with shell access to the container is a trusted colleague.
It is **not** acceptable for adversarial multi-tenancy, regulated environments
that require per-user audit trails, or any deployment where users must not
see each other's DAGs.

The two phases below close those gaps:

- **Phase 11** introduces user-aware auth: identifying who is calling, what
  they own, and running their steps under their own UID rather than root.
  This is what closes the "scheduled-but-private" gap from §2.
- **Phase 12** opens up a second supported deployment shape — a small
  self-hosted daggle behind TLS with a login — without otherwise changing the
  trust model.

---

## 2. The CLI-vs-daemon split, and the gap it leaves

Today daggle's CLI (`daggle run`, `daggle history`, etc.) and its daemon
(`daggle serve`) read and write **completely separate state directories** by
default. The CLI uses each user's per-user XDG paths (e.g.
`~/.config/daggle/`, `~/.local/share/daggle/`); the daemon uses whatever the
operator pointed it at via `DAGGLE_CONFIG_DIR` and `DAGGLE_DATA_DIR`.

Same engine, different bookkeeping. That produces a real gap when daggle is
deployed in a multi-user data-science environment:

| | CLI / local | Registered via daemon |
|---|---|---|
| Private to one user | ✅ | ❌ |
| Visible / triggerable by others | ❌ | ✅ |
| Can be scheduled by the cron daemon | ❌ | ✅ |
| Runs under that user's UID | ✅ (the invoker) | ❌ (always root today) |
| Shows in the dashboard / archives | ❌ | ✅ |

The empty cell — **"scheduled, but private to me, and run as me"** — has no
answer today. To get scheduling you must register a project with the daemon,
and registering means root execution and visibility to every R session in the
container. There's no middle ground for "I want a personal hourly pipeline
that touches only my home directory."

That gap is what Phase 11 fixes.

---

## 3. Phase 11 — Multi-user safety

Phase 11 turns daggle from "trusted-network single-tenant" into
"multi-user-safe with per-user execution". This is the phase that closes the
gap from §2: it makes "scheduled, but private to me, and run as me" possible.

The work decomposes into three problems. Each is independent in scope but
they have to land together to be useful.

### 3.1 The three problems

| Problem | Question it answers | Lives in |
|---|---|---|
| **Authentication** | *Who* is calling the API? | Auth middleware in front of every handler |
| **Authorisation** | *What* can this caller do? | Per-endpoint check against `projects.yaml` ownership |
| **Identity propagation** | *Whose UID does the step run under?* | The executor when it spawns step subprocesses |

The first two are HTTP-layer concerns and are well-understood territory —
dagu already does them (§6). The third one is where daggle has to do
something dagu doesn't: actually drop privilege when running steps. That's
the harder part of this phase.

### 3.2 Authentication — *who* is calling

The daemon needs to resolve every API call to a specific Unix user. Five
plausible mechanisms; each has different operational shape:

| Option | How it works | Pros | Cons |
|---|---|---|---|
| **A. PAM** (Pluggable Authentication Modules — the same system Linux uses for `login`/`ssh`) | daggle authenticates a `username + password` against the host's PAM stack. One source of truth: existing Unix accounts. | Reuses existing accounts; no new credentials to manage. | Cgo dependency on `libpam`; daggle has to handle plaintext passwords briefly; Linux/macOS only. |
| **B. File-based token per user** | Admin runs `daggle user add alice`; daggle generates a token and stores it in `$DAGGLE_CONFIG_DIR/users.yaml` (mode 0600). daggleR sends `Authorization: Bearer <token>`. Daemon resolves token → user. | Self-contained, portable, easy to audit. | Yet another credential to rotate; admin must manage the user list manually. |
| **C. Trusted reverse proxy header** | An HTTP proxy (Caddy + oauth2-proxy, nginx + Kerberos, …) does the actual auth and forwards `X-Remote-User: alice`. daggle trusts the header **only** when a `--trust-proxy` flag is set and the connection comes from a configured proxy IP. | Plugs into any external IDP (OIDC, SAML, LDAP) without daggle implementing them. | Mis-configured trust = impersonation hole. Adds a deployment dependency. |
| **D. JWT with builtin user store** (dagu's `builtin` mode) | daggle has its own user table (file-based or sqlite); accepts `POST /auth/login` with username+password, returns a signed JWT (JSON Web Token — a self-contained session token). Subsequent requests send `Authorization: Bearer <jwt>`. | Self-contained, browser-friendly, works for daggleR too. | daggle becomes responsible for password hashing, rotation, OIDC integration, account lockout — significant scope. |
| **E. Unix-socket peer credentials** | daggle listens on a Unix domain socket (no TCP). When a connection comes in, the kernel reports the connecting process's UID via `SO_PEERCRED`. daggle trusts that UID directly — no password needed. | Kernel-attested identity; no credentials to manage; impossible to impersonate from outside the host. | Only works for callers on the same host. daggleR would need to switch from HTTP-over-TCP to HTTP-over-Unix-socket. |

For the **PWB scenario specifically**: every R session lives in the same
container as the daemon, so option **E** is unusually attractive — the kernel
can vouch for who's calling, no password, no token, no proxy. Combine it with
**B** (file tokens) for programmatic / out-of-container clients, and you
cover both cases without implementing PAM or a JWT user store. Option D
matches what dagu does, so it's a defensible "match the leader" choice if we
ever want browser logins.

For the **self-hosted browser-facing scenario** (Phase 12 territory): D or
C is the right shape. E doesn't apply because callers are remote.

### 3.3 Authorisation — *what* they can do

Once we know the caller's identity, this part is mechanical. `projects.yaml`
(today just a list of registered projects) grows an **owner** field:

```yaml
projects:
  - name: alice-personal
    path: /home/alice/projects/personal
    owner: alice
  - name: company-reports
    path: /opt/shared/dags/reports
    owner: pipelines-bot
```

A small list of admin usernames lives in `config.yaml`:

```yaml
auth:
  admins:
    - root
    - jdoe
```

State-mutating endpoints (`POST /runs`, `DELETE /projects/...`,
`POST /cancel`, `POST /approve`) check `caller == owner OR caller in
admins`. Otherwise → `403 Forbidden`. Read endpoints (`GET /dags`,
`GET /runs`) filter results to projects the caller can see.

This pattern is **RBAC** (Role-Based Access Control — rules for who can do
what). daggle's version is intentionally minimal: two roles (owner, admin)
and one resource type (project). dagu has a richer model (5 roles,
workspace scoping); we can grow into that later if we need to. **Open
question: do we want a `team` / group concept, or stick with single-owner?**

### 3.4 Identity propagation — running steps as the caller

This is the hard part. When alice triggers her DAG, the step subprocess
should run **as `alice`**, not as root or as the daggle daemon. The kernel
then enforces filesystem boundaries via standard Unix permissions: alice's
step can read alice's home, can't read bob's. **No daggle-level sandboxing
is required for this** — Unix already does it.

The implementation question is *how* the daemon transitions privilege.
Seven options on the table; only the first three are realistic for a single
shared daemon:

| # | Option | How it works | Pros | Cons | Verdict |
|---|---|---|---|---|---|
| 1 | **Daemon-as-root + `setuid` before exec** | Daemon runs as root. When it `exec`s a step, it sets `Cmd.SysProcAttr.Credential` (Go syscall API) to the target UID/GID before `exec`. Linux atomically switches identity at process start. | Simplest possible model; no extra binaries; daemon is already root in the PWB deployment. | The whole daemon is privileged. A bug = root compromise. | **Recommended starting point.** |
| 2 | **Setuid helper binary** | Daemon runs as a low-privilege user `daggle`. To start a step, it `exec`s a small helper binary `daggle-step` that has the setuid bit set. The helper validates inputs, drops to the target UID, then `exec`s the actual step. | Privileged code surface is a single small binary that can be audited rigorously. Daemon itself doesn't need root. | Setuid binaries are notoriously hazardous — env-var inheritance, `LD_PRELOAD`, `PATH` lookup, working-directory resolution all become attack vectors. Writing one safely is real engineering. | Hardening upgrade after option 1 ships. |
| 3 | **`sudo` to per-user worker** | Daemon runs unprivileged. For each step it runs `sudo -u alice -- /usr/local/bin/daggle-worker <step-spec>`. A `/etc/sudoers.d/daggle` allows `daggle → alice, bob, ...` with NOPASSWD. Worker reads the spec from stdin or a temp file. | Reuses well-audited `sudo` machinery. Sudoers config is the explicit privilege boundary. | Requires sudoers to be configured correctly; one daemon-side bug + a permissive sudoers = privilege escalation. Each step is a `sudo` invocation (forks an extra process, parses sudoers — fast but not free). | Viable alternative to option 1 for sites that prefer sudo as their privilege primitive. |
| 4 | **Per-user systemd `--user` units** | Each user runs their own `daggle serve` instance under `systemctl --user`, on their own port, with their own state. A separate "shared" daemon runs company-wide DAGs. | OS does the isolation; no daggle code change. | Process zoo (one daemon per logged-in user). User services don't run when user isn't logged in by default (`loginctl enable-linger` required). Doesn't fit the shared-scheduler model — every user becomes their own ops surface. | Doesn't match the "one daemon for the team" goal. Rule out. |
| 5 | **Container per step** | Each step runs in a transient Docker container with `--user $(id -u alice)`. | Strong isolation via OS containers. | Cold-start overhead per step; requires Docker available; complicates `Rscript` / renv (host R installation isn't visible inside the container without bind-mounts). | Too heavy for typical R-script workloads. The existing `docker:` step type already covers the cases where it's worth it. |
| 6 | **`CAP_SETUID` capability** | Daemon runs as non-root but is granted Linux capability `CAP_SETUID` (and `CAP_SETGID`). It can then `setuid()` to any user without being root. | Less than full root, on paper. | `CAP_SETUID` is effectively equivalent to root for impersonation — no real security benefit, but more deployment friction. | No upside over option 1. Skip. |
| 7 | **User namespaces / bubblewrap** | Daemon spawns step in a new Linux user namespace; maps step's UID to target user inside the namespace. | Kernel-enforced isolation beyond just UIDs. | Heavy machinery; Linux-only; user namespaces are sometimes disabled in containers; conflicts with renv path resolution. | Out of scope. Phase 13+ if ever. |

**Recommended path: option 1 first, option 2 as a hardening upgrade once the
first version proves stable.** Option 1 keeps the daemon's process model
unchanged from today (it's already root in PWB) and makes a single, focused,
auditable change to the executor. Option 2 is the right *destination* but
adds enough net-new attack surface (a setuid binary) that doing it second,
with a working option-1 implementation as the comparison baseline, is a lot
less risky than doing it first.

### 3.5 Worked example: a shared daemon, two users

After Phase 11 ships, a single daggle daemon hosts both:

- **alice's personal pipeline.** Project `alice-personal` is owned by
  `alice`. Only alice and admins see it in `daggle list-dags`. When the
  scheduler fires its hourly cron, the step process runs as alice (option 1:
  daemon `setuid`s before `exec`); if the step tries to read
  `/home/bob/data.csv` it gets `EACCES` from the kernel. alice's run history
  shows up in *her* daggleR session and the dashboard filters it
  accordingly.
- **The company-wide reporting pipeline.** Project `company-reports` is
  owned by a service account `pipelines-bot`. Only `pipelines-bot` (or
  admins) can trigger or edit it. Steps run as `pipelines-bot`. Anyone with
  read access to that project sees its run history; nobody but admins or the
  bot can mess with it.

Both share the same scheduler, the same dashboard, and the same archive
machinery. The split is purely an authorisation + UID-propagation matter.

---

## 4. Phase 12 — Self-hosted deployment shape

Phase 12 adds a second supported posture: a small-scale, single-node,
self-hosted daggle brought up with `docker compose up` and reachable from a
browser with TLS (encrypted HTTPS). The on-prem PWB defaults stay untouched
(loopback bind, no auth) so existing deployments don't need to change.

This phase is **independent of Phase 11**. Three of its items — bind address,
auth mode, base path — are what currently block daggle from being deployed as a
service on a managed multi-user R host at all; see
[§9.2](#92-blocking-gaps-for-shape-b). The self-hosted posture works in
single-tenant mode (one shared password or token); if Phase 11's user-aware
auth is also installed, it works in multi-user mode too. Either order ships
fine.

### What changes

Items marked **shipped** are in the tree today; see `docs/api.md` for the
operator-facing reference.

- ✅ **shipped** — **Configurable bind address** — `--bind` / `DAGGLE_BIND_ADDR` /
  `server.bind`, default `127.0.0.1`. Opt in to `0.0.0.0` for "listen on all
  interfaces".
- ✅ **shipped** — **Single-tenant auth modes** — `DAGGLE_AUTH_MODE=none|basic|token`,
  also `--auth-mode` and `server.auth.mode`. Basic uses one shared
  username/password. Token uses one shared bearer token, auto-generated and
  persisted to `$DAGGLE_DATA_DIR/auth/token` (mode 0600) on first start and
  reused on every later start. Both accept `password_file` / `token_file`
  variants for credentials that arrive as decrypted files. These modes layer
  on top of Phase 11's user-aware auth as a "single-tenant fallback" for
  deployments that don't want per-user identity.
- **TLS termination in daggle** — `DAGGLE_TLS_CERT_FILE` +
  `DAGGLE_TLS_KEY_FILE` for the no-reverse-proxy case.
- ✅ **shipped** — **Reverse-proxy awareness** — `DAGGLE_BASE_PATH` / `--base-path`
  for sub-path mounting behind Caddy/nginx, with the UI's own links emitted
  under the prefix; `--trust-proxy` to honour `X-Forwarded-*` headers.
- ✅ **shipped** (bar the TLS check, which waits on TLS) — **Safety guardrails** —
  daggle refuses to start in unsafe combinations: non-loopback bind +
  `auth=none`, an unknown auth mode, basic mode with no credentials, or a
  configured credential file that is missing or empty.
- **First-class Docker images** — `ghcr.io/cynkra/daggle` (~50 MB,
  HTTP/shell/Quarto DAGs) and `ghcr.io/cynkra/daggle-r` (rocker/r-ver +
  renv, full R step support). GoReleaser publishes both on tag.
- **Compose templates** — `deploy/docker/compose.minimal.yaml` (loopback,
  no auth) and `deploy/docker/compose.selfhost.yaml` (daggle + Caddy with
  Let's Encrypt TLS, token auth by default).
- **`daggle token generate`** — one-shot CLI helper.
- **`daggle doctor` deploy section** — reports bind, auth mode, TLS state,
  base path; warns near guardrail trips.
- **An unauthenticated liveness probe** — ✅ **shipped** as `/healthz`, the one
  route exempt from auth, so a container healthcheck works before any
  credential exists. `/api/v1/health` keeps the detail and stays behind auth.
- **daggleR auth** — companion R package honours `DAGGLE_API_TOKEN` and
  `DAGGLE_API_BASIC_USER`/`_PASSWORD`. Tracked separately in the daggleR
  repo.

---

## 5. How dagu and other tools handle this

Useful prior art for both phases. The TL;DR: dagu has done all of Phase 12
and the HTTP-layer parts of Phase 11, but explicitly **does not** propagate
identity to step execution. So Phase 11's §3.4 work is genuinely new
territory relative to our cited reference.

### dagu

dagu (the YAML-based DAG scheduler in Go we cite as our spiritual ancestor)
documents four authentication modes:

- `none` — no auth.
- `basic` — single shared username + password (HTTP Basic). Their docs
  explicitly say *"Basic mode provides a single shared credential — for
  multi-user support with RBAC, use `auth.mode: builtin`"*.
- `builtin` (default) — JWT sessions, with a builtin user table.
  Self-hosted user CRUD; passwords stored on disk; auto-generated initial
  admin via `POST /api/v1/auth/setup`. **Five roles**: admin, manager,
  developer, operator, viewer. Optional per-workspace scoping.
- `oidc` — layered on builtin. Auto-enabled when OIDC client config is
  set. Documented providers: Google, Auth0, Keycloak.

All four also support **API keys** as bearer tokens, each carrying a role
and optional workspace scope.

dagu's RBAC is real and well-shaped at the HTTP layer. **What dagu does not
do**:

- **No identity propagation to steps.** A GitHub code search of
  `dagu-org/dagu` returns zero hits for `syscall.Credential`,
  `SysProcAttr`, `setuid`, or `RunAs`. Step subprocesses run as the dagu
  daemon's own OS user. The "user who triggered" is purely an app-level
  concept; the kernel never sees it.
- **No `runAs` / `user` field on steps.** The dagu YAML spec has
  `working_dir` and `env` but nothing to set the executing UID. The only
  `user:` field is on container steps (`user: "1000:1000"` for
  Docker/K8s) — that's container-image isolation, not host identity.
- **No multi-tenant deployment story.** The Helm chart is explicitly a
  single shared instance; no namespace-per-user or shared-PVC isolation
  pattern. The nearest thing is in-app workspace scoping in RBAC.
- **No FS sandboxing or home-directory scoping.** Their hardening guide
  is purely app-layer (use `builtin` auth, enable TLS, set
  `permissions.write_dags: false`).

So if we copy dagu's auth-modes naming (we already are: `none`, `basic`,
`token`) and their five-role RBAC shape, we get to Phase 11's HTTP-layer
parts essentially "for free in design space". The piece we'd be inventing
is §3.4 — the executor side that actually runs steps under a non-root UID.

### Airflow, Prefect, Dagster (briefly)

Heavyweight orchestrators handle this differently and they're not the model
we want, but worth one paragraph for context:

- **Airflow** has a `run_as_user` field on tasks and historically used
  `sudo` to switch identity. The pattern is similar to our option 3 but
  configured per-task, not per-DAG. The Kubernetes executor runs each task
  in a pod with a configurable `securityContext` (UID, GID,
  capabilities) — so isolation comes from the pod, not from the
  scheduler-as-process.
- **Prefect / Dagster** lean on the worker-pool model: workers can be
  per-team or per-environment, each running with its own credentials.
  Identity propagation is a deploy-time concern (which worker pool runs
  this code?), not a runtime concern.

The pattern we want is closer to Airflow's `run_as_user` than to the
worker-pool model — daggle is a single-node tool and the answer should
match that.

---

## 6. What's *not* in either phase

The following Phase 13 ("Scale") items from `design.md` are **independent of
the auth / multi-user thread and of the self-hosted deployment shape**, and
can ship on their own track:

- **State compaction** (`daggle compact`) — keeps history performant on
  long-running DAGs.
- **Distributed workers** — coordinator/worker model across machines.
- **Queue system** — concurrency limits with queue overlap policy.
- **Prometheus metrics** — scheduler + run metrics.
- **SSH remote execution** — run steps on remote machines.
- **R session pooling** — keep warm `Rscript` processes for fast inline
  expressions.

Tracked in `design.md`, not in this doc.

---

## 7. Phase 11 vs Phase 12 — what's shared, what's not

The two phases share **one** piece: an HTTP middleware that attaches a
caller identity to each request. The middleware's interface is the same
across modes:

```go
type Caller struct { Username string; IsAdmin bool }
func WithCaller(next http.Handler) http.Handler  // attaches *Caller to req.Context
```

What's behind the middleware differs by mode:

- Phase 12 single-tenant: the middleware checks the shared password or
  token, attaches a single synthetic caller (e.g. `Username: "operator"`).
- Phase 11 multi-user: the middleware resolves the credential to a real
  Unix user (via §3.2 option E / B / etc.) and attaches that.

Either order ships fine. If Phase 12 ships first, Phase 11 swaps in a
richer middleware implementation without touching any handler code.

---

## 8. Open questions

Decisions that still need to be made before either phase becomes concrete
tickets.

**Process model for non-root execution.** §3.4 lays out three viable
options (1, 2, 3). Recommendation is option 1 first, but the user's
operations team may have a strong sudo-only or strong unprivileged-daemon
preference that pushes us to 3 or 2 respectively.

**Source of truth for user identity.** §3.2 lists five mechanisms.
Recommendation is E (Unix-socket peercred) for in-container callers + B
(file tokens) for out-of-container, but PAM (A) is a real alternative if
the deployment already manages users via PAM and we want one source of
truth.

**Group / team ownership.** §3.3 keeps it to single-owner per project. Do
we want a `members:` list, or a separate `groups.yaml`, or stick with
single-owner and let admins act as the gap-filler?

**Self-hosted-with-multi-user.** Phase 11 gives us multi-user execution but
needs an identity source. Phase 12's self-hosted posture has no obvious
host-OS account integration (Docker container with no PAM, no real user
list). What's the "self-hosted multi-user" combination — a flat
file-based user table (option B) plus the per-user worker model (option
3)? Or do we declare self-hosted to be single-tenant only?

**On-prem PWB sidecar evolution.** Once Phase 11 lands, the existing
"trusted-network, no per-user identity" mode can either:

- become opt-in legacy mode (`DAGGLE_AUTH_MODE=none` continues to work; the
  on-prem deployment keeps using it), or
- be dropped in favour of always-on per-user identity, accepting that
  existing deployments need a config change.

The first is friendlier; the second is cleaner.

**Backward-compatibility for state.** When an existing deployment upgrades,
how do existing runs and projects (which have no `owner` field) appear?
Reasonable default: assign them all to a synthetic `legacy` admin owner,
prompt the operator to reassign during a quiet maintenance window.

**Who decides the executing uid when the executor is remote?** If daggle grows
the coordinator/worker split that managed multi-user R hosts need (§9.1
shape b), §3.4 no longer describes one process. Either the coordinator resolves
the caller to a uid and the worker trusts the dispatch, or the worker
re-authenticates. Whichever we pick, the coordinator↔worker channel becomes
part of the trust boundary (§9.4) — and that constrains the protocol, so it
wants deciding first.

**Is there a "never execute locally" mode?** dagu forces
`default_execution_mode: distributed` so a run cannot silently execute in a
container without R (§9.3). Should daggle refuse local execution whenever a
worker is configured, and should that be the default rather than opt-in?

**Where does run history live?** In shape (b) the executor container is
recreated on every iteration bump. Schedules, queue, and history have to sit
with the scheduler; run *logs* are written by the worker. That implies a shared
volume with a mixed-uid write discipline, which is a known sharp edge in
comparable deployments: the DAG directory ends up with three classes of writer
and the UI quietly stops listing what it cannot read.

**Can the whole configuration be generated?** Anything daggle writes to itself
on first start (a generated token, a user table, a migrated state file) is
invisible to a template-driven estate (§9.5). Is "every setting expressible in
one rendered file, no interactive setup step" a constraint we are willing to
hold?

---

## 9. Deployment profile: managed multi-user R hosts

Everything above is written in the abstract. This section pins it to the
deployment profile daggle is actually expected to land in, because that profile
rules some of the options above out and makes others nearly free. It describes a
class of environment, not one installation — but it is the class we build for
first, so treat it as the acceptance criteria for Phases 11 and 12.

The profile, in one paragraph: a single host runs a Docker Compose stack
generated from templates by a provisioning repo. One reverse proxy terminates
TLS and serves every other service under a sub-path of a host FQDN; nothing else
publishes a port. Alongside shared infrastructure (database, directory server,
identity provider) the host runs one or more **multi-user R server containers**
— a Posit Workbench container or equivalent — each pinned to a dated environment
snapshot, so several generations of toolchain coexist on one host. Users are real
Unix accounts, provisioned from LDAP or AD.

Three properties of that profile constrain daggle more than the topology does.

**Containers are disposable; the generated config is the source of truth.** A
host is rebuilt by re-rendering templates and bringing the stack back up.
Anything not in the provisioning repo or in a declared volume does not survive.
Configuration therefore has to be a file a template can generate — not state a
daemon writes to itself on first start.

**Secrets arrive as encrypted files in the repo**, decrypted by each container's
entrypoint at start into mode-0400 files under a runtime directory. There is no
secret manager daemon in the stack. daggle's `${file:...}` env source is exactly
the right primitive here; `${vault:...}` is dead weight for this profile.

**The R environment lives in exactly one container.** See §9.3 — this is the
constraint that shapes everything else.

### 9.1 Two candidate shapes, and what each demands

**(a) Sidecar inside the R server container — the shape §1 already describes.**
`daggle serve` runs as a supervisord program inside the Workbench container: the
binary is baked into the image, config is rendered to `/etc/daggle/`, and a
program file is dropped into the image's supervisord include directory. Loopback
binding is fine, nothing is exposed, and **daggle supports this shape today with
no new code** — it is how comparable schedulers get deployed on these hosts as an
interim measure.

Its limits are why that interim measure does not last: the scheduler's lifecycle
is welded to one environment snapshot's container, run history dies when that
container is recreated on the next toolchain bump, and one snapshot becomes
"special" for reasons unrelated to what it contains.

**(b) Standalone service plus workers inside the R containers.** The scheduler,
web UI, and a dispatch coordinator run in their own container; each R server
container runs a passive worker that long-polls the coordinator and executes
dispatched steps locally, making only outbound connections. This is the shape
that survives snapshot churn, and the one daggle would have to adopt to be a
first-class service on such a host. It is also, not coincidentally, the shape
dagu moved to for the same reasons (§5). **daggle cannot do this today** — there
is no worker/coordinator split (`design.md` Phase 13), so a standalone daggle
would try to execute steps inside its own container. §9.3 covers why that fails
outright rather than merely degrading.

### 9.2 Blocking gaps for shape (b)

Checked against the current tree (`internal/cli/serve.go`, `internal/cli/serveconfig.go`, `api/`). The first four shipped in the `feat/deployable-serve` work; the rest are open:

| Requirement | Why | Status |
|---|---|---|
| Configurable bind address | Inside a container, loopback is container-local, so the reverse proxy — a *different* container — could never reach it. | ✅ `--bind` / `DAGGLE_BIND_ADDR` / `server.bind` |
| Auth on by default | The proxy publishes the service on a public FQDN. An unauthenticated "run arbitrary R and shell" API is not deployable. | ✅ `basic` and `token` modes, and a bind guardrail that makes exposure without one impossible |
| Sub-path mounting | Services live at `https://<fqdn>/<subpath>/`. The proxy forwards `/<subpath>/` without stripping the prefix when the app knows its own root (dagu's `base_path`, authentik's `AUTHENTIK_WEB__PATH`); stripping instead breaks every absolute link the app emits. | ✅ `--base-path` / `DAGGLE_BASE_PATH` / `server.base_path`; UI links are emitted with the prefix |
| `X-Forwarded-*` handling | TLS terminates at the proxy; daggle sees plain HTTP plus `X-Forwarded-Proto: https`. Redirects, absolute links, and cookie `Secure` flags must follow the forwarded scheme. | ✅ `--trust-proxy` / `server.trust_proxy`, opt-in so headers are never honoured from a direct client |
| Published OCI image, stable binary path | Images are pinned by tag and tracked by an automated updater; the R image installs a matching CLI by copying the binary straight out of the server image at build time. That needs a published image, a fixed path for the binary, and a static build (`CGO_ENABLED=0` already holds). | **Missing.** Phase 12 `ghcr.io/cynkra/daggle{,-r}` |
| `PUID` / `PGID` remapping | Shared volumes get written by the daemon, by workers, and by named users, each with a different uid. An entrypoint that honours `PUID`/`PGID` lets a host align the service's identity with local convention without rebuilding the image. | **Missing** |
| Coordinator/worker version lock | Both sides should come from the same pinned tag, and the protocol needs an explicit version check on connect — a mixed-version pair must refuse to run rather than misbehave. | N/A until workers exist |

TLS *inside* daggle (Phase 12's `DAGGLE_TLS_CERT_FILE`) is not needed for this
profile — the proxy owns certificates — which makes it the lowest-value Phase 12
item here. Bind address, auth, and base path were the three that unblocked
anything at all, and they are now in place; what remains before shape (b) is
real is the image and the worker split.

### 9.3 Why steps cannot execute in daggle's own container

The R server container is the only place on such a host with a usable execution
environment, and that is structural rather than incidental. It holds the R
versions, the Python interpreters, the system libraries and database drivers, the
decrypted secrets — and the user accounts. Users are resolved through NSS/sssd
against LDAP or AD, the directory client itself runs under that container's
supervisord stack, and the container's entrypoint owns home-directory creation
driven by `getent passwd`. A second container started from the *same image* does
not inherit any of that, because the provisioning happens at container start, not
at build time.

So a standalone daggle executing steps locally would run `Rscript` in an image
with no R, no named users, and no home directories. dagu's answer is
`default_execution_mode: distributed`, which forces every run through the
coordinator precisely so that an unlabelled DAG cannot silently execute in the
scheduler's own container. daggle needs an equivalent, and it should **fail
loudly** rather than attempt a local run: the failure it prevents ("why does my
DAG report `Rscript: not found` when R is obviously installed?") is otherwise
very expensive to diagnose.

### 9.4 Identity: what this profile gives you, and what shape (b) breaks

Good news for §3.4. Inside the R server container the users are real Unix users:
NSS resolves them, PAM works, `getent passwd alice` returns a home directory, and
supervisord already runs its children as root. That makes **option 1
(daemon-as-root, `setuid` before `exec`) directly implementable**, makes **option
A (PAM)** a genuine choice rather than a theoretical one, and makes **option E
(Unix-socket peercred)** work for R sessions, since they are processes in the
same container as the daemon.

Bad news: shape (b) splits the authenticator from the executor, which §3.4
implicitly assumes are one process. With the coordinator in the standalone
service and the worker in the R container, identity has to travel over the wire —
the coordinator authenticates alice, the dispatch carries "run this as alice",
and the *worker* performs the `setuid`. That turns the worker into a
privilege-granting surface: it must not accept a bare uid from an unauthenticated
peer, which pulls the coordinator↔worker channel inside the trust boundary. The
usual deployment of that channel is unencrypted h2c on an internal container
network, which is acceptable only as long as nothing on it grants privilege —
exactly the property identity propagation removes. This wants deciding **before**
the worker protocol is designed, not after.

It is also where daggle can be better than the incumbent rather than merely
equivalent. Comparable schedulers run every step as root with `HOME=/root` and
treat per-user execution as out of scope (§5); on a host where a dozen analysts
share one container, that is the gap that matters most.

### 9.5 Auth configuration has to be render-time

A builtin user store with browser-driven first-admin setup (dagu's `builtin` mode
and its `POST /api/v1/auth/setup`) fits this profile badly: the account exists
only in a volume, so it is invisible to the provisioning repo, lost on a volume
reset, and not reproducible when the host is rebuilt from templates. Two shapes
do fit:

- credentials rendered into the config file from per-host variables and encrypted
  secrets — what comparable services do for their basic-auth credentials;
- Phase 11 option B's `users.yaml`, which can live encrypted in the provisioning
  repo and be decrypted into the config directory by the entrypoint.

If Phase 12 auto-generates a token into `$DAGGLE_DATA_DIR/auth/token` on first
start, it needs two properties to be operable here: it must be **overridable** by
an env var or a file the template supplies, and when generated it must land in a
**declared volume**. A secret that only ever exists inside a container is one the
operator cannot hand to daggleR clients or rotate from the repo.

The general rule: every setting must be expressible in one generated config file,
and no start-up path may require an interactive step.

### 9.6 What this suggests about ordering

1. ~~**`--bind`, an auth mode, `DAGGLE_BASE_PATH`, forwarded-proto handling.**~~
   **Shipped.** Individually small — a flag, a middleware, a path prefix — and
   together they were the difference between "can be deployed as a service on
   such a host" and "cannot". Config lives in a `server:` block so the whole
   posture is expressible in one generated file (§9.5), and the guardrails make
   an unauthenticated exposed daggle a startup error rather than an incident.
2. **A published image with a stable binary path and a pinned tag.** Also cheap,
   and it is what lets the R image install a CLI that matches the server.
3. **§3.4 identity propagation.** The piece comparable tools do not have, and the
   R server container is the one place where it is both implementable and worth
   having.
4. **Worker/coordinator split.** The large one; only worth starting once 1–3
   exist, and it should be designed with §9.4's trust question settled.

Shape (a) needs none of this. If daggle should reach a managed host before any of
the above lands, the in-container sidecar is the route.

---

## See also

- [`design.md`](design.md) — full roadmap including phases not covered here.
- [`architecture.md`](architecture.md) — how daggle's components fit together
  today.
- [`api.md`](api.md) — current REST API surface (which Phase 11 will
  authorise per-caller).
- Internal deployment notes for the managed-host platform (private) — the
  concrete instance of the §9 profile, including the per-host variables and
  generated Compose/Dockerfile templates a deployment would need.
