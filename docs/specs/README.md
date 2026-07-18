# flexconf behaviour specifications

These documents specify the observable behaviour of the `flexconf` toolkit as
derived from the existing source. They describe **what each component does** —
its inputs, outputs, errors, side effects, and invariants — independently of the
implementation, so they can serve as a reference for callers and as a contract
for future changes.

Module: `github.com/sylvanld/flexconf`

## Components

| Spec | Package | Responsibility |
| --- | --- | --- |
| [settings.md](settings.md) | `settings` | Resolve where an application keeps its configuration. |
| [secrets.md](secrets.md) | `secrets` | High-level secret store (`Store`) over a pluggable `Driver`. |
| [keepass-driver.md](keepass-driver.md) | `secrets` | `Driver` backed by a password-protected KeePass `.kdbx` file. |
| [agent.md](agent.md) | `agent` | Background process holding an unlocked driver, served over a per-user Unix socket, plus a matching client. |
| [secretcli.md](secretcli.md) | `cli/secrets` | Cobra command tree wiring settings + secrets + agent into any app's CLI. |

## Layering

```
settings ─────────────► resolves paths (kdbx file, socket dir, agent log)
                 │
secrets ─────────┤ Store ──► Driver ──┬─► KeepassDriver (real .kdbx access)
                 │                     └─► agent.Client  (forwards over socket)
                 │
agent ───────────┤ Server holds an unlocked Driver; Client *is* a Driver
                 │
cli/secrets ─────┘ builds the whole thing as a cobra command tree
```

The unifying abstraction is `secrets.Driver`: both the real KeePass backend and
the agent client implement it, so the `Store` is agnostic about whether it talks
to a local file or to a background agent over a socket.
