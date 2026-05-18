# Kitchen — project memory for Claude Code

This file is loaded into context at the start of every Claude Code session
in this repo. Keep it short, factual, and skewed toward "things you can't
discover by reading the code".

---

## What kitchen is

A small developer toolbox written in Go. CLI + Bubble Tea TUI. Connects to a
Kubernetes cluster (via `client-go`, not by shelling out to `kubectl`) and
provides quick views over deployments, logs, etc.

Today's subcommands:

| Command                    | Purpose                                                                 |
| -------------------------- | ----------------------------------------------------------------------- |
| `kitchen log`              | Three-screen TUI: namespace → deployment → live log viewer              |
| `kitchen webtop`           | Top-level menu (list / deploy / undeploy)                               |
| `kitchen webtop list`      | Live picker of running webtops with coreo backend, PR + tag links       |
| `kitchen webtop deploy`    | Three-step wizard: pick build → pick coreo → name → apply k8s.yml       |
| `kitchen webtop undeploy`  | Remove a kitchen-created webtop (filtered by `managed-by=kitchen` label)|
| `kitchen upgrade`          | Self-update from GitHub Releases                                        |
| `kitchen version`          | Print version, commit, build date                                       |

---

## Integrates with two upstream apps

Kitchen is purpose-built around two services living in the `mafin`
Kubernetes namespace:

| App      | Role      | Repo                                            |
| -------- | --------- | ----------------------------------------------- |
| coreo    | backend   | https://github.com/finforce/mafin-coreo         |
| webtop   | frontend  | https://github.com/finforce/mafin-coreo-app     |

These repos are **read-only references** for the kitchen project. We use
them as sources of truth for how deployments are produced (image names,
container conventions, Ingress patterns, env vars, …). **Do not commit,
push, or open PRs against these repos under any circumstance** — even
fixes to obvious bugs belong upstream, not in this side-project's
working copy.

### Pipeline reference doc

The deployment conventions kitchen relies on are documented in
[`docs/upstream-pipelines.md`](docs/upstream-pipelines.md). Everything
kitchen does that touches coreo or webtop has to be **in alignment**
with the assumptions described there. If you change a kitchen feature
that interacts with these apps, re-read that document first.

The document drifts over time as upstream pipelines evolve. Periodically
(e.g. once per minor kitchen release, or whenever a coreo/webtop deploy
behaves unexpectedly):

1. Refresh the pipeline doc using the procedure in its "Keeping this
   document current" section.
2. Audit kitchen's image / env-var / ingress assumptions against any
   changes you find.
3. Update kitchen's code where the assumption no longer holds, and add a
   test that locks the new shape.

---

## Project structure

```
cmd/kitchen/        binary entrypoint
internal/
  cli/              cobra commands (log, webtop, upgrade, version, root)
  k8s/              client-go wrapper — plain-Go types out
  recents/          MRU store for namespace/deployment picker
  tui/
    components/     shared title bar, status bar
    log/            three-screen TUI (namespace → deployment → log viewer)
    styles/         lipgloss theme (colors, prefab styles)
  updater/          self-update from GH releases
  version/          ldflags-injected build info
docs/
  upstream-pipelines.md   ← READ THIS before touching coreo/webtop logic
scripts/install.sh   one-liner installer
.goreleaser.yaml     four-platform release config (linux/darwin × amd64/arm64)
DECISIONS.md         design decisions log
```

---

## Conventions

- All code, identifiers, comments and commit messages in English. Czech is
  fine in interactive conversation with the user.
- Conventional Commits (`feat:`, `fix:`, `refactor:`, `chore:`, `docs:`, …).
- Errors are wrapped with `fmt.Errorf("...: %w", err)`. No `panic` outside
  `main`.
- Bubble Tea: every API call lives in a `tea.Cmd`. The `Update` loop never
  blocks on I/O.
- Pickers identify webtop / coreo deployments by **container image repo**,
  not by Deployment name. Names are CI artifacts and can be renamed; the
  image repo on GHCR is the project's identity.

## Build & test

```sh
go build ./cmd/kitchen     # produces ./kitchen
go test ./...              # all unit tests
go vet ./...
```

CI runs `go vet`, `golangci-lint`, `go test -race`. Releases are
GoReleaser-driven on `v*` tags.

## Releases

We've been cutting frequent micro releases (`v0.1.x`, `v0.2.x`) as
features land. Each release is tagged and triggers `.github/workflows/release.yml`,
which builds four archives + `checksums.txt` and publishes them to GitHub
Releases. `kitchen upgrade` consumes these.

When in doubt about whether to release immediately or batch, ask.
