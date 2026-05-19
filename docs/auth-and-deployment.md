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

daggle currently supports exactly one deployment shape: the **on-prem PWB
sidecar**. In that shape:

- `daggle serve` runs as a `supervisord` process inside a Posit Workbench (PWB)
  container.
- The HTTP API binds to **127.0.0.1** (the loopback address — only callers on
  the same machine can reach it). The port is never exposed outside the
  container.
- There is **no authentication**. Every R session inside the container can
  call any API endpoint as an anonymous, equally-privileged caller.
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

This phase is **independent of Phase 11**. The self-hosted posture works in
single-tenant mode (one shared password or token); if Phase 11's user-aware
auth is also installed, it works in multi-user mode too. Either order ships
fine.

### What changes

- **Configurable bind address** — `--bind` / `DAGGLE_BIND_ADDR`, default
  `127.0.0.1`. Opt in to `0.0.0.0` for "listen on all interfaces".
- **Single-tenant auth modes** — `DAGGLE_AUTH_MODE=none|basic|token`. Basic
  uses one shared username/password. Token uses one shared bearer token,
  auto-generated and persisted to `$DAGGLE_DATA_DIR/auth/token` (mode 0600)
  on first start. These modes layer on top of Phase 11's user-aware auth as
  a "single-tenant fallback" for deployments that don't want per-user
  identity.
- **TLS termination in daggle** — `DAGGLE_TLS_CERT_FILE` +
  `DAGGLE_TLS_KEY_FILE` for the no-reverse-proxy case.
- **Reverse-proxy awareness** — `DAGGLE_BASE_PATH` for sub-path mounting
  behind Caddy/nginx; `--trust-proxy` to honour `X-Forwarded-*` headers.
- **Safety guardrails** — daggle refuses to start in unsafe combinations
  (non-loopback bind + `auth=none`, asymmetric TLS cert/key, basic mode with
  no credentials).
- **First-class Docker images** — `ghcr.io/cynkra/daggle` (~50 MB,
  HTTP/shell/Quarto DAGs) and `ghcr.io/cynkra/daggle-r` (rocker/r-ver +
  renv, full R step support). GoReleaser publishes both on tag.
- **Compose templates** — `deploy/docker/compose.minimal.yaml` (loopback,
  no auth) and `deploy/docker/compose.selfhost.yaml` (daggle + Caddy with
  Let's Encrypt TLS, token auth by default).
- **`daggle token generate`** — one-shot CLI helper.
- **`daggle doctor` deploy section** — reports bind, auth mode, TLS state,
  base path; warns near guardrail trips.
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

---

## See also

- [`design.md`](design.md) — full roadmap including phases not covered here.
- [`architecture.md`](architecture.md) — how daggle's components fit together
  today.
- [`api.md`](api.md) — current REST API surface (which Phase 11 will
  authorise per-caller).
