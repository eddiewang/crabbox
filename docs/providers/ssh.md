# Static SSH Provider

Read when:

- choosing `provider: ssh`, `provider: static`, or `provider: static-ssh`;
- reusing an existing Linux, macOS, or Windows host instead of provisioning one;
- changing `internal/providers/ssh` or static-host sync behavior.

Static SSH is the provider for machines Crabbox does **not** create. The backend
resolves a configured SSH target and hands it to core, which owns sync, command
execution, results, tunnels, and status rendering. There is no provisioning,
cleanup, or cost accounting — the host's lifecycle is yours.

The provider id is `ssh`, with aliases `static` and `static-ssh`. It is
direct-only and is never brokered through the coordinator.

## When To Use

Use Static SSH when:

- the machine already exists and should not be provisioned by Crabbox;
- you want to target a local Mac, LAN host, lab VM, or persistent Windows box;
- cloud provider cleanup and cost guardrails do not apply.

Use AWS, Azure, Google Cloud, or Hetzner when you want Crabbox to create and
delete the machine for you.

## Quick Start

```sh
crabbox run --provider ssh --static-host buildbox.local -- pnpm test
crabbox ssh --provider ssh --id buildbox.local
crabbox run --provider static-ssh --target windows --static-host win-dev.local \
  -- pwsh -NoProfile -Command '$PSVersionTable'
```

`warmup` for Static SSH does not provision a machine. It validates the
configured target and returns it as a lease-like object so the rest of the
warm-box workflow (`run`, `ssh`, `status`, tunnels) behaves the same as for
provisioned providers.

`stop` for a static lease removes only the local claim. It never touches the
host. There is no `cleanup` action.

## Targets

Static SSH supports all four targets:

- `linux`
- `macos`
- `windows` with `windows.mode: normal` (PowerShell over OpenSSH, archive sync)
- `windows` with `windows.mode: wsl2` (POSIX contract inside WSL)

`target` and (for Windows) `windows.mode` must match the real host — Crabbox
cannot infer whether a Windows host runs native PowerShell or WSL2 commands.
On Linux, macOS, and WSL2 targets, Crabbox's workspace-owner protocol invokes
`/bin/sh` explicitly and does not require the SSH account to use a POSIX login
shell on POSIX hosts; zsh, Bash, and Fish login shells are supported there.
WSL staging supports Windows OpenSSH with `cmd.exe`, Windows PowerShell, or
PowerShell (`pwsh`) as DefaultShell. Preparation binds the observed shell kind
to the fresh route nonce; PowerShell executes the verified script directly to
preserve raw stderr and the workload exit code. Unknown shell kinds fail closed.

## Configuration

The static target lives under the `static:` block. SSH credentials fall back to
the shared `ssh:` block when the matching `static:` field is empty.

### Linux

```yaml
provider: ssh
target: linux
static:
  host: buildbox.local
  user: crabbox
  port: "22"
  workRoot: /work/crabbox
```

### macOS

```yaml
provider: ssh
target: macos
static:
  host: mac-studio.local
  user: alice
  port: "22"
  workRoot: /Users/alice/crabbox
```

When no generic or `static.workRoot` override is configured, a macOS target
uses `/Users/<resolved-user>/crabbox`, where `<resolved-user>` is the final SSH
user after applying `ssh.user` and `static.user` precedence.

### Windows (native)

```yaml
provider: ssh
target: windows
windows:
  mode: normal
static:
  host: win-dev.local
  user: builder
  port: "22"
  workRoot: C:\crabbox
```

### Windows (WSL2)

```yaml
provider: ssh
target: windows
windows:
  mode: wsl2
static:
  host: win-dev.local
  user: builder
  port: "22"
  workRoot: /home/builder/crabbox
```

Intentional workspace-owner background helpers start a separate Linux session
on WSL and retain their existing child witness, token, and expiry checks. The
staged command waits for detachment before returning. Ordinary foreground
children remain subject to stage cancellation and group cleanup. Direct WebVNC's
retained websockify process similarly uses a separate session under its existing
PID/start-time/boot/nonce identity checks. This does not add general POSIX
workspace descendant containment or require `setsid` on macOS.

### Config fields

| `static:` key | Purpose |
| --- | --- |
| `host` | SSH host or IP (required). |
| `user` | SSH user. Falls back to `ssh.user`, then `$USER`. |
| `port` | SSH port. Falls back to `ssh.port`; the base default is `2222` with a `22` fallback. |
| `workRoot` | Remote checkout/work directory. |
| `id` | Optional stable lease id (default derived from `host`). |
| `name` | Optional friendly slug (default derived from `host`). |

The SSH private key comes from the shared `ssh.key` field (or `CRABBOX_SSH_KEY`).
There is no per-host key field; the static provider connects with your existing
key, not a key Crabbox generates.

A repository-defined `static.host` cannot silently inherit a key or ambient SSH
authentication from user config, the environment, an SSH agent, or local SSH
config. Define `static.host` and a relative, symlink-resolved `ssh.key` file
contained by the repository in the same repository config, or approve the
destination explicitly with `--static-host` or `CRABBOX_STATIC_HOST`. Absolute,
missing, and repository-escaping key paths require explicit host approval.

### Flags

```text
--static-host
--static-user
--static-port
--static-work-root
```

### Environment

```text
CRABBOX_STATIC_HOST
CRABBOX_STATIC_USER
CRABBOX_STATIC_PORT
CRABBOX_STATIC_WORK_ROOT
CRABBOX_STATIC_ID
CRABBOX_STATIC_NAME
CRABBOX_SSH_USER
CRABBOX_SSH_KEY
CRABBOX_SSH_PORT
```

## Host Requirements

POSIX hosts (Linux, macOS, WSL2) need:

- SSH access for the configured user;
- `git`, `rsync`, `tar`, and `sh`;
- a writable `static.workRoot`;
- desktop/browser/code tooling only if those capabilities are requested.

Windows native hosts need:

- the OpenSSH server;
- PowerShell;
- `tar` for archive sync;
- VNC/browser tooling only if desktop flows are requested.

WSL2 hosts additionally need:

- WSL installed and reachable through `wsl.exe`, with Linux tooling inside the
  default distribution and `static.workRoot` set to a WSL path;
- the Windows OpenSSH server's SFTP subsystem enabled so Crabbox can stage WSL2
  workloads before one-shot execution.

Verify both the WSL runtime and SFTP transport before a long run:

```sh
crabbox doctor --provider ssh --target windows --windows-mode wsl2 \
  --static-host win-dev.local --doctor-probe-ssh
```

If `wsl2-sftp` fails, configure `Subsystem sftp internal-sftp` in the Windows
OpenSSH `sshd_config`, restart the Windows `sshd` service, and rerun Doctor.
Connection loss and malformed protocol responses remain transport errors rather
than being mislabeled as a missing subsystem.

The staged launcher supports both `cmd.exe` and PowerShell as the Windows
OpenSSH default shell. Its complete encoded command stays below 8191 bytes.
Encoding prevents outer-shell expansion; it does not provide secrecy. Workload
scripts and sensitive payload bytes remain in the private stage, not the
launcher command line.

The staged file is one finite envelope: a bounded descriptor, a Windows owner,
a Linux helper, the command, and binary input. The launcher binds its complete
length and SHA-256 digest, including the descriptor. The private `CBXFLAT2`
descriptor is 80 bytes: version, length, and limit fields occupy its first 48
bytes, followed by 32 cryptographically random blinding bytes generated once per
spool. The blinder stays inside the private envelope across retries; it is never
included in launcher arguments or route proofs. Every prefix containing program,
command, or input bytes includes the entire blinder, keeping exposed integrity
digests from revealing predictable payloads. SFTP validates the fresh
nonce-root proof before sensitive writes, then uploads once and checks regular-file
metadata and exact size before publication. It does not download the envelope
again: the mandatory native verifier is the full-content authority. Size-scaled
transfer allowances count one upload, not an upload plus readback. The launcher verifies and consumes
the file through the same exclusive Windows handle. Ready files, route proofs,
and acknowledged partial uploads use that same verifier for discard; partial
uploads must match the corresponding exact prefix of the retained local spool.
Identity means nonce plus expected content, not a persistent creation ID: a
byte-identical copy is equivalent, but different content is never deleted.
Unknown objects are not swept by age. An unacknowledged create, changed partial,
or uncertain publication requires cleanup investigation and never authorizes replay.

Windows sends the helper and finite input through bounded asynchronous WSL pipe
writes through an unbuffered view of the same stdin handle. Initial stdin opening
and helper delivery share a 15-second cap measured from launcher startup, clipped
to the remaining original execution deadline (12 seconds for control calls). Neither step resets
that clock. Later command/input writes retain their transfer idle limit: 2 seconds
for control calls, 15 seconds otherwise. Cleanup handoff is clipped to its own
existing 10-second deadline; unlimited workloads still have bounded startup.
Failure phases distinguish launcher startup, pipe opening/flushing, and helper writing. In these
diagnostics, `expected` is the command/input length; `read` and `written` count
workload bytes from completed reads and writes. They exclude the helper and do
not measure kernel progress during an unfinished write. Windows PowerShell
5.1 remains supported: its Framework StreamWriter uses the console input
encoding, so the launcher declares and flushes its preamble first. The bounded
bootstrap accepts exactly the declared empty preamble or UTF-8 BOM before the
unchanged helper bytes; other preambles fail closed. Core uses explicit UTF-8
without a BOM. No console encoding is changed by production. The helper is fully read before execution; it needs no installed loader
or drive automount. Windows keeps its single writer open after frame completion.
Linux materializes finite command/input files, then gives the control descriptor
only to a launcher-loss watcher. Workloads do not inherit that descriptor.
An independent Linux supervisor directly parents an in-group guard and the
workload leader. Cleanup revalidates guard PID, start identity, group, and record
before TERM and KILL, reaps its children, and removes evidence only after actual
group absence. Fallback cleanup asks the surviving supervisor to stop; it never
reconstructs signal authority from a pathname after that supervisor dies.
Supervisor loss, a missing witness, or an unreaped group zombie leaves evidence and
reports cleanup ambiguity. This transport containment does not change the POSIX
workspace-owner protocol's separate direct-child ownership contract.

WSL2 staging requires a private Windows HOME owned by the SSH user, SYSTEM, or
Builtin Administrators. The `.crabbox` parent and `wsl-stage` directory must be
owned by the SSH user. Access may be granted only to that user, SYSTEM, and
Builtin Administrators; Crabbox does not change HOME ownership or ACLs. Crabbox
rejects files, reparse points, and existing unsafe ACLs before changing
permissions or writing a route proof or payload.
Both safe inherited staging directories are normalized to an explicit SSH-user
owner and protected inheritable DACL on preparation. Directories already matching
the full private ACL policy are validated without rewriting their owner or DACL;
a fresh nonce proof checks that each SFTP route reaches the same protected
Windows root.
Each configured route has a bounded preparation, transfer, and cleanup budget;
with multiple routes, a no-input reachability probe shares the preparation budget
and may fail over without cleanup because it creates no state. Once nonce-proof
preparation starts, fallback requires exact owned cleanup and is allowed only
before publication. A successful probe does not authorize retrying a mutation.
Closed-pipe failures during SFTP teardown follow the same fallback rules as
connection loss. Permission, integrity, collision, ambiguous publication, and
failed cleanup errors remain terminal.

If an existing staging directory or its parent was permissive, quiesce the
target (including untrusted processes and their open handles) before repairing
its ACLs or removing it for safe recreation. Tightening an ACL alone does not
revoke existing handles and does not establish safe staging. Crabbox does not
automatically repair unsafe existing directories or HOME permissions.

## Capabilities

| Capability | Support |
| --- | --- |
| SSH | yes |
| Crabbox sync | yes |
| `cp` | yes on POSIX and WSL2 targets (rsync over resolved SSH) |
| `tunnel` | yes (local and remote loopback only) |
| Desktop / browser / code | host-dependent (requires the tooling installed on the host) |
| Actions hydration | Linux hosts only |
| Tailscale | use the host's existing tailnet address or MagicDNS name |
| Coordinator (brokered) | never — direct-only |

## Gotchas

- Crabbox never cleans up static hosts. Disk, processes, and leftover state are
  yours to manage.
- Static hosts drift. Run `crabbox doctor --provider ssh` and a small
  `crabbox run` before long jobs.
- The provider connects with your configured SSH key; it does not mint a
  per-lease key the way provisioned providers do.

## Related

- [Provider reference](README.md)
- [Provider backends](../provider-backends.md)
- [Sync](../features/sync.md)
- [SSH keys](../features/ssh-keys.md)
- [SSH lease transport](../features/ssh-transport.md)
