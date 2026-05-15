# Upstream pipelines — coreo & webtop

This document captures what kitchen knows about the deployment pipelines of
the two upstream applications it integrates with. Kitchen does **not** own
these pipelines — it only reads the state they produce in the cluster. The
invariants documented here are what kitchen relies on to identify deployments,
locate their URLs, link them to their backends, etc.

> **Important:** Whenever a kitchen feature touches one of these apps,
> verify the assumption it relies on against this document. When the upstream
> pipelines change, update this document **first**, then make kitchen's code
> consistent. See "Keeping this document current" at the bottom.

Last refreshed: 2026-05-15 (against `main` of both repos).

---

## Applications

| App      | Role     | Repo                                            | Container image                          |
| -------- | -------- | ----------------------------------------------- | ---------------------------------------- |
| coreo    | backend  | https://github.com/finforce/mafin-coreo         | `ghcr.io/finforce/mafin-coreo`           |
| webtop   | frontend | https://github.com/finforce/mafin-coreo-app     | `ghcr.io/finforce/mafin-coreo-app`       |

Both are deployed into a shared Kubernetes cluster (current context: `dev` /
`staging` / `production`) in the **`mafin`** namespace.

---

## Shared invariants

These hold for both apps and are what kitchen depends on most.

### Image-based identity

The application's identity is its container image repository on GHCR. Tags
vary (review-app slugs, release versions, digests for production) but the
repo path is stable across review-apps, staging and production. Kitchen
identifies apps by image, not by Deployment name (see
`internal/cli/webtop.go::isWebtopImage` for the canonical pattern).

### Name convention

Within a single deployment of an app:

```
Deployment.name == Service.name == Ingress.spec.rules[*].http.paths[*].backend.service.name
```

For review-apps the name is `<app-prefix>-<SUFFIX>` (e.g. `mafin-coreo-app-feat-foo`).
For staging there is no suffix — staging is updated in place via
`kubectl set image -n mafin deployment/<app-prefix> ...`, so the resource
name is literally `mafin-coreo` or `mafin-coreo-app`.

### SUFFIX and NONCE

Review-app manifests are rendered with `envsubst` substituting two variables:

- `${SUFFIX}` — a slug derived from the branch / ref via `finforce/actions-base@main`'s
  `EFFECTIVE_SLUG` output (max 45 chars, lowercase, `[^a-z0-9-]` → `-`).
- `${NONCE}` — `$GITHUB_RUN_ID`; surfaces as a pod label so re-deploys roll the
  ReplicaSet even if the image tag didn't change.

### Ingress host pattern

Both apps use traefik with a wildcard cert on `*.mafin.finforce.dev`. There
is **no `spec.tls` in the Ingress objects** — TLS is provided externally by
the ingress controller, but the URLs are reached via HTTPS in practice.
Kitchen prepends `https://` unconditionally.

| App    | Host pattern                              |
| ------ | ----------------------------------------- |
| coreo  | `coreo-<SUFFIX>.mafin.finforce.dev`       |
| webtop | `webtop-<SUFFIX>.mafin.finforce.dev`      |

### Backend linkage

Each webtop instance has the URL of the coreo it talks to embedded in its
pod spec as the **`MAFIN_URL` env var** (a literal `value:`, not `valueFrom`).
This is the source of truth kitchen uses to map webtops to coreos in
`kitchen webtop`.

---

## coreo (backend, Java / Spring Boot)

### Container

| Field             | Value                                          |
| ----------------- | ---------------------------------------------- |
| Container name    | `mafin-coreo`                                  |
| Container port    | `8080` (named `http-alt`)                      |
| Probes            | liveness + readiness on `GET /actuator/health` |
| Mounted secret    | `mafin-coreo` → `/app/config` (read-only)      |

Notable env vars set in the pod spec:

```
MANAGEMENT_ENDPOINTS_WEB_EXPOSURE_INCLUDE = *
MANAGEMENT_ENDPOINT_HEALTH_SHOW_DETAILS   = always
MAFIN_COREO_ENVIRONMENT                   = development
OTEL_{TRACES,METRICS,LOGS}_EXPORTER       = none
```

### Workflows

| File                       | Trigger                                  | What it does                                                                                  |
| -------------------------- | ---------------------------------------- | --------------------------------------------------------------------------------------------- |
| `ci.yml`                   | push to main, PR, release published      | Maven build + tests, JaCoCo coverage, builds & pushes Docker image, then runs deploys:        |
|                            |                                          | • `test-deployment` on push to main                                                            |
|                            |                                          | • `redeploy-if-active` on PRs whose issue has the `review-app` label                          |
|                            |                                          | • `staging-deployment` on release (in-place `kubectl set image`)                              |
|                            |                                          | • `production-container` on release (pushes image to Artifact Registry for prod)              |
| `deploy-review-app.yml`    | `workflow_dispatch`                      | Renders `k8s.yml` with the ref's slug, applies it to the cluster, comments the URL on the PR. |
| `deploy-on-comment.yml`    | `issue_comment` containing `/deploy`     | Validates PR is from same repo + waits up to 30 min for the `build` job, then triggers `deploy-review-app.yml` with the PR head ref. Adds `review-app` label and a 🚀 reaction. |
| `undeploy-review-app.yml`  | `pull_request: closed` with `review-app` | Renders `k8s.yml` and `kubectl delete -f` it; removes the `review-app` label.                  |

### Key resources & paths in the cluster

```
Deployment mafin-coreo[-<SUFFIX>]   .spec.template.spec.containers[0]
                                       .image = ghcr.io/finforce/mafin-coreo:<tag>
                                       .name  = mafin-coreo
Service    mafin-coreo[-<SUFFIX>]   port 80 → targetPort 8080
Ingress    mafin-coreo[-<SUFFIX>]   rules[0].host = coreo-<SUFFIX>.mafin.finforce.dev
```

---

## webtop (frontend, Node.js / Next.js)

### Container

| Field           | Value                                  |
| --------------- | -------------------------------------- |
| Container name  | `mafin-coreo-app`                      |
| Container port  | `3000`                                 |
| Probes          | none in the rendered manifest           |
| Mounted secret  | none                                   |

Notable env vars set in the pod spec:

```
NODE_ENV                 = production
APP_ENV                  = dev | production    (production manifests must NOT set MAFIN_TOKEN — CI guards this)
NEXT_TELEMETRY_DISABLED  = "1"
MAFIN_URL                = https://coreo-<…>.mafin.finforce.dev    ← backend pointer kitchen reads
NEXT_PUBLIC_SENTRY_DSN   = …
MAFIN_TOKEN              = … (dev only — guarded against in production by CI)
```

### Workflows

| File                       | Trigger                                  | What it does                                                                                  |
| -------------------------- | ---------------------------------------- | --------------------------------------------------------------------------------------------- |
| `ci.yml`                   | push to main, PR, release published, `workflow_dispatch` | Lint / format / typecheck / unit tests / Storybook UI tests / build / Docker image push; then deploys per branch/release. Includes a step that fails CI if `APP_ENV=production` and `MAFIN_TOKEN` is set in `k8s*.yml`. |
| `retrigger-on-coreo-comment.yml` | issue_comment in the *coreo* repo  | Looks for `coreo: https://…` URLs in the PR comments to wire the webtop review-app to a custom coreo backend (the URL becomes `MAFIN_URL`). |
| `undeploy-review-app.yml`  | `pull_request: closed`                   | Renders `k8s.yml` with the slug and `kubectl delete -f`.                                       |

### Key resources & paths in the cluster

```
Deployment mafin-coreo-app[-<SUFFIX>]   .spec.template.spec.containers[0]
                                            .image = ghcr.io/finforce/mafin-coreo-app:<tag>
                                            .name  = mafin-coreo-app
                                            .env[MAFIN_URL] = <coreo URL>
Service    mafin-coreo-app[-<SUFFIX>]   port 80 → targetPort 3000
Ingress    mafin-coreo-app[-<SUFFIX>]   rules[0].host = webtop-<SUFFIX>.mafin.finforce.dev
```

---

## How kitchen relies on this

| Kitchen feature      | Assumption                                                                 |
| -------------------- | -------------------------------------------------------------------------- |
| `kitchen webtop` identifies webtop deployments | image starts with `ghcr.io/finforce/mafin-coreo-app` |
| `kitchen webtop` reads the COREO column        | env var `MAFIN_URL` on the webtop container, as a literal value |
| `kitchen webtop` reads the WEBTOP URL column   | Ingress in the same namespace whose backend service name equals the deployment name; host is `webtop-<SUFFIX>.mafin.finforce.dev`, served as HTTPS |
| `kitchen log` shows pods of a deployment       | standard k8s deployment → ReplicaSet → Pod label selectors |

Any change to the upstream pipelines that breaks one of these assumptions
will silently produce wrong output. Pay attention when:

- The container image moves to a different registry / repo path.
- The container name changes (less critical — kitchen matches by image).
- The MAFIN_URL env var is sourced from a ConfigMap / Secret (`valueFrom`)
  rather than set inline — kitchen skips `valueFrom` for safety, so the
  COREO column would show `(no coreo)`.
- The Ingress host pattern changes, or the Deployment/Service/Ingress name
  convention stops being uniform.

---

## Keeping this document current

Refresh procedure (run periodically, e.g. each minor kitchen release):

1. Fetch both repos' workflows via `gh`:
   ```
   gh api repos/finforce/mafin-coreo/contents/.github/workflows --jq '.[].name'
   gh api repos/finforce/mafin-coreo-app/contents/.github/workflows --jq '.[].name'
   ```
2. Diff against the descriptions above. Pay special attention to:
   - container images and names
   - env vars (especially `MAFIN_URL`)
   - Ingress host pattern
   - resource-name conventions (review-app vs staging vs production)
3. Update this doc with whatever changed and bump the "Last refreshed" date.
4. Audit kitchen's code (`internal/cli/webtop.go`, `internal/k8s/*`) for
   anything that depended on the old assumption and adjust.
5. Add tests covering the new shape if the change is meaningful.

Do **not** push commits or open PRs in the upstream repos — kitchen treats
them as read-only references.
