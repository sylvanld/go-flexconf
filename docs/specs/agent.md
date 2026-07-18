# `agent` — unlock agent server and client

Package `agent` provides a background process (`Server`) that holds an
already-unlocked [`secrets.Driver`](secrets.md) in memory for a bounded time and
serves its operations over a per-user Unix socket, plus a `Client` that itself
implements `secrets.Driver` by forwarding calls over that socket. This lets a CLI
unlock once and avoid re-prompting for the master password on every invocation.

The agent serves **writes as well as reads**, so a single unlock covers every
command — at the cost of the served driver retaining the master credentials in
memory for the agent's lifetime.

## Socket location

### `SocketPath(appName string) string`

- Prefers `$XDG_RUNTIME_DIR/<appName>/agent.sock` — a private, per-user, `0700`
  tmpfs that avoids the symlink/TOCTOU hazards of a shared `/tmp`.
- Falls back to `<os.TempDir()>/<appName>-<uid>/agent.sock` when
  `XDG_RUNTIME_DIR` is unset.

## Wire protocol

Newline-delimited JSON, one request and one response per connection.

**Operations** (`Request.Op`): `get`, `set`, `list`, `delete`, `status`, `lock`.

```go
type Request struct {
    Op     string          // one of the operations above
    Key    string          // for get/delete
    Secret *secrets.Secret // for set
}

type Response struct {
    OK      bool
    Secret  *secrets.Secret  // get
    Secrets []secrets.Secret // list
    ErrKind string           // stable error code (below)
    Err     string           // human-readable message
}
```

### Error propagation across the socket

So that `errors.Is` keeps working on the client side, driver errors are mapped to
a stable `ErrKind` on the wire and reconstructed by the client:

| `ErrKind` | Reconstructed as |
| --- | --- |
| `not_found` | `secrets.ErrNotFound` |
| `empty_key` | `secrets.ErrEmptyKey` |
| `read_only` | `secrets.ErrReadOnly` |
| `internal` | `errors.New(msg)` (or `"agent: internal error"` if empty) |

## `Server`

```go
type Server struct {
    Driver      secrets.Driver // already Unlock()-ed
    SocketPath  string
    IdleTTL     time.Duration  // default 5m
    MaxLifetime time.Duration  // default 30m
    // ... unexported synchronisation state
}
```

Build with `NewServer(driver, socketPath)`.

### `Listen() error`

Binds the Unix socket; idempotent (no-op if already listening).

1. Creates the socket's parent directory `0700`.
2. If a socket file already exists:
   - If a live agent answers on it → refuses with `agent: already running at <path>`.
   - Otherwise the stale socket file is removed.
3. Binds (`net.Listen("unix", …)`) and `chmod`s the socket to `0600`. A chmod
   failure closes the listener and returns the error.

`Serve` calls `Listen` automatically; call it explicitly first when you want the
bind to succeed/fail **before** entering the blocking serve loop (the CLI does
this so a refused start reports an error instead of a misleading "listening"
line).

### `Serve() error`

- Returns `secrets.ErrNoDriver` if no driver is set.
- Lazily initialises channels and calls `Listen` if needed.
- Starts an accept loop, then blocks selecting on:
  - **activity** — any request resets the idle timer.
  - **idle timer** (`IdleTTL`) firing → shutdown.
  - **max lifetime timer** (`MaxLifetime`) firing → shutdown, regardless of
    activity (an absolute cap).
  - **done** (from `Stop`) → shutdown.
- Always removes the socket and drops the driver reference before returning.

Expiry summary: the agent locks when **either** the idle timeout (reset on use)
**or** the absolute max lifetime elapses, or on an explicit lock/stop.

### `Stop()`

Triggers an orderly shutdown once (idempotent via `sync.Once`); used on signals
and by the `lock` request path.

### Shutdown (`shutdown`)

Runs once: closes the listener, removes the socket file, and sets `Driver = nil`
so the decrypted store can be garbage-collected. (Go strings cannot be reliably
wiped; dropping the reference plus a short TTL is the actual mitigation.)

### Connection handling

For each accepted connection (each handled in its own goroutine):

1. **Peer check** (`checkPeer`) — see access control below. On failure the
   response is `{OK:false, ErrKind:internal, Err:"access denied"}` and the
   connection closes.
2. Reads one newline-terminated JSON request. A malformed request yields
   `{… Err:"bad request"}`.
3. Records activity (non-blocking) to reset the idle timer.
4. Dispatches and writes one response.
5. If the op was `lock`, calls `Stop()` after replying.

### Dispatch

Guarded by a mutex. If the driver has already been dropped, every op returns
`{… Err:"agent locked"}`.

| Op | Behaviour |
| --- | --- |
| `status`, `lock` | `{OK:true}` (no driver call). `lock` additionally triggers shutdown after the reply. |
| `get` | `Driver.Get(key)`; on success `{OK:true, Secret}`. |
| `list` | `Driver.List()`; on success `{OK:true, Secrets}`. |
| `set` | Requires `Secret != nil` (else `{… Err:"set requires a secret"}`); `Driver.Set(*Secret)`. |
| `delete` | `Driver.Delete(key)`. |
| unknown | `{… Err:"unsupported operation: <op>"}`. |

Driver errors are returned with their mapped `ErrKind` and message.

## `Client`

```go
type Client struct {
    SocketPath  string
    DialTimeout time.Duration // default 2s
}
```

Build with `NewClient(path)`. Implements `secrets.Driver` (compile-time asserted),
so `secrets.NewStore(client)` makes the agent transparent to the store.

Each method opens a fresh connection (`net.DialTimeout`), sends one JSON request,
reads one JSON response, and closes. A non-`OK` response is turned back into an
error via the `ErrKind` mapping.

| Method | Op | Notes |
| --- | --- | --- |
| `IsRunning() bool` | — | `true` if the socket accepts a connection. Used to decide whether to start an agent. |
| `Unlock() error` | `status` | Just verifies reachability; the agent is already unlocked, so no password is exchanged. |
| `Get(key)` | `get` | |
| `List()` | `list` | |
| `Set(secret)` | `set` | |
| `Delete(key)` | `delete` | |
| `Lock() error` | `lock` | Asks the agent to shut down and drop cached secrets. |

## Access control & hardening

### Peer authentication (`checkPeer`)

- **Linux**: verifies the connecting peer's uid via `SO_PEERCRED`; rejects any
  uid different from this process's uid (`agent: peer uid <n> not permitted`).
  This is the real access control — socket file permissions are not reliably
  enforced on connect.
- **Non-Linux**: no-op; access is gated only by the `0700` directory and `0600`
  socket, a weaker guarantee.

### `Harden() error`

Best-effort, intended to be called once by the agent process at startup:

- **Linux**: sets `RLIMIT_CORE` to 0 (no core dumps), `PR_SET_DUMPABLE` 0, and
  `mlockall(MCL_CURRENT|MCL_FUTURE)` to keep pages out of swap. Failures are
  returned for logging but are not fatal (`mlockall` may be denied without
  sufficient `RLIMIT_MEMLOCK`).
- **Non-Linux**: no-op.

## Security model (from package/README notes)

The agent trades a little safety for convenience: decrypted secrets **and the
master key** live in memory for the TTL window rather than milliseconds. It
defends against *other* users (peer-uid check, private socket) and against
secrets reaching disk (no core dumps, `mlockall`), and keeps the window short. It
does **not** defend against code running as the same user (`ptrace`, keylogging)
or root. A short TTL — not memory scrubbing — is the real mitigation.

## Invariants

- On any exit path, `Serve` removes the socket file and releases the driver.
- Only same-uid peers are served (on Linux).
- The idle timer is reset by every accepted request; the max-lifetime cap cannot
  be extended by activity.
