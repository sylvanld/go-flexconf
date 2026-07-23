---
icon: lucide/keyboard
tags:
  - reference
  - secrets
---

# Credential prompting

Package `github.com/sylvanld/go-flexconf/flexprompt` collects input values —
typically credentials — from wherever your program runs: an interactive
terminal, CI environment variables, or canned values in tests. Components that
need input *declare* what they need; the single installed `Prompter` answers
everything in one interaction.

Normative spec: [prompter.md](../specs/prompter.md).

## Quick start

```go
import "github.com/sylvanld/go-flexconf/flexprompt"

func main() {
    // Interactive apps opt in once at startup:
    flexprompt.SetPrompter(flexprompt.NewCLIPrompter())
    // …
}
```

If no prompter is installed, `GetPrompter()` returns a safe default whose
`Dispatch` fails every **required** request with `ErrNoPrompter` — a daemon
never hangs reading stdin by accident.

## The `Prompter` interface

```go
type Prompter interface {
    Dispatch(ctx context.Context, reqs []PromptRequest) (map[string]string, error)
}
```

`Dispatch` answers **all** requests in a single interaction and returns the
answers keyed by `PromptRequest.ID`:

- An **optional** request with no value is omitted from the result.
- A **required** request with no value fails with `ErrPromptUnavailable`.
- On any non-nil error the returned map is `nil` — never read it.
- Duplicate or empty request IDs are programming errors and fail loudly.

### `PromptRequest`

| Field | Meaning |
|-------|---------|
| `ID` | Stable machine key (`"password"`). The join key between declaration and answers; non-interactive prompters use it as the lookup key. |
| `Label` | Human-facing text for interactive prompters. |
| `Secret` | Masked input; never echoed or logged. |
| `Optional` | Value may be absent (omitted from answers). |
| `Default` | Suggested value for interactive prompters. Ignored when `Secret` is true. |
| `Confirm` | Interactive double-entry (new passwords); entries must match. Ignored by non-interactive prompters. |

## The process-wide singleton

```go
func SetPrompter(p Prompter) // install once at startup; nil resets to default
func GetPrompter() Prompter  // never nil; default fails required requests with ErrNoPrompter
```

Prompting is a *process*-level concern (where am I running?), so there is
exactly one prompter, not one per vault. Both accessors are safe for
concurrent use. `SetPrompter(nil)` restores the default — handy between tests.

## Built-in prompters

| Constructor | Use case | Behaviour |
|-------------|----------|-----------|
| `NewCLIPrompter(opts...)` | interactive terminal | Asks each request in turn; masks `Secret` input (via `x/term`); honours `Default` and `Confirm`. |
| `NewMapPrompter(values)` | tests, presets | Resolves each `ID` from the map. |
| `NewEnvPrompter(prefix)` | CI | Resolves `ID` from `prefix + ID` uppercased, dashes → underscores (`FLEXCONF_` + `keyfile-passphrase` → `FLEXCONF_KEYFILE_PASSPHRASE`). |
| `PrompterFunc(fn)` | adapters | Wraps a function as a `Prompter`. |

`NewCLIPrompter` accepts `WithCLIStreams(in *os.File, out io.Writer)` to
redirect its terminal streams (defaults `os.Stdin` / `os.Stderr`).

## Errors

```go
var (
    ErrPromptCancelled   error // user aborted; terminal — callers stop retrying
    ErrPromptUnavailable error // required request unresolved (non-interactive)
    ErrNoPrompter        error // no prompter installed; call SetPrompter
)
```

Match with `errors.Is`; messages are not part of the contract.
