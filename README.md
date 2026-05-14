# kitchen

A developer toolbox with a modern Bubble Tea TUI. The first feature, `kitchen
log`, streams live Kubernetes logs from every pod of a deployment into a
single colour-coded viewer. JIRA and GitHub integrations will be added later.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/jiripisa/kitchen/main/scripts/install.sh | sh
```

The script detects your OS/arch (linux & darwin, amd64 & arm64), verifies the
release checksum, and installs to `/usr/local/bin` (using `sudo` if needed),
falling back to `$HOME/.local/bin` when the system path isn't writable.

## Usage

```sh
kitchen log       # pick namespace → pick deployment → stream logs
kitchen upgrade   # check for a newer release and replace the running binary
kitchen version   # print version, commit, build date
```

### `kitchen log`

A three-screen TUI:

1. **Namespace picker** — filterable list of all namespaces in the current
   context. Type to filter, `↑`/`↓` to move, `Enter` to pick, `q`/`Esc` to quit.
2. **Deployment picker** — deployments in the selected namespace, with ready
   counts. `Esc` goes back.
3. **Log viewer** — live logs from every pod, each line prefixed with a
   per-pod colour. `f` toggles follow on/off, `g`/`G` jumps to top/bottom,
   `Esc` goes back, `q` quits.

### Prerequisites

`kitchen log` reads `KUBECONFIG`, falling back to `~/.kube/config`. The
current context is what gets used — switch with `kubectl config use-context`
beforehand.

## Development

```sh
git clone https://github.com/jiripisa/kitchen
cd kitchen
go build ./cmd/kitchen
./kitchen log
```

Run the test suite:

```sh
go test ./...
```

## Screenshot

_TODO — add a recording of the log viewer in action._

## License

[MIT](./LICENSE)
