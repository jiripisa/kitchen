package k8s

import (
	"strings"
	"testing"
	"time"
)

// A trimmed-down copy of `mafin-coreo-app/k8s.yml` — just enough structure
// to exercise the substitution + label injection. Keeping the secret token
// out so this file can be checked in without leaking anything.
const webtopTemplate = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: mafin-coreo-app-${SUFFIX}
  namespace: mafin
  labels:
    nonce: "${NONCE}"
spec:
  replicas: 1
  selector:
    matchLabels:
      app: mafin-coreo-app-${SUFFIX}
  template:
    metadata:
      labels:
        app: mafin-coreo-app-${SUFFIX}
        nonce: "${NONCE}"
    spec:
      containers:
        - env:
            - name: MAFIN_URL
              value: "${COREO_URL}"
          image: ghcr.io/finforce/mafin-coreo-app:${SUFFIX}
          name: mafin-coreo-app
          ports:
            - containerPort: 3000
              protocol: TCP
---
apiVersion: v1
kind: Service
metadata:
  name: mafin-coreo-app-${SUFFIX}
  namespace: mafin
spec:
  ports:
    - port: 80
      protocol: TCP
      targetPort: 3000
  selector:
    app: mafin-coreo-app-${SUFFIX}
  type: ClusterIP
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: mafin-coreo-app-${SUFFIX}
  namespace: mafin
spec:
  ingressClassName: traefik
  rules:
    - host: webtop-${SUFFIX}.mafin.finforce.dev
      http:
        paths:
          - backend:
              service:
                name: mafin-coreo-app-${SUFFIX}
                port:
                  number: 80
            path: /
            pathType: Prefix
`

func TestRenderWebtopManifest(t *testing.T) {
	dep, svc, ing, err := renderWebtopManifest(WebtopDeploySpec{
		Suffix:    "feat-foo",
		ImageTag:  "feat-foo",
		CoreoURL:  "https://coreo-bar.mafin.finforce.dev",
		Branch:    "feat/foo",
		CreatedBy: "alice",
		Template:  []byte(webtopTemplate),
	})
	if err != nil {
		t.Fatalf("renderWebtopManifest: %v", err)
	}

	// Names follow `mafin-coreo-app-<suffix>` for all three.
	if got, want := dep.Name, "mafin-coreo-app-feat-foo"; got != want {
		t.Fatalf("deployment name: got %q, want %q", got, want)
	}
	if got, want := svc.Name, "mafin-coreo-app-feat-foo"; got != want {
		t.Fatalf("service name: got %q, want %q", got, want)
	}
	if got, want := ing.Name, "mafin-coreo-app-feat-foo"; got != want {
		t.Fatalf("ingress name: got %q, want %q", got, want)
	}

	// Image tag is decoupled from suffix even though we passed identical
	// strings — verify the rename worked by checking it ends with the tag,
	// not by guessing the order of replaces.
	img := dep.Spec.Template.Spec.Containers[0].Image
	if got, want := img, "ghcr.io/finforce/mafin-coreo-app:feat-foo"; got != want {
		t.Fatalf("image: got %q, want %q", got, want)
	}

	// MAFIN_URL = the coreo URL we passed.
	gotMafin := ""
	for _, e := range dep.Spec.Template.Spec.Containers[0].Env {
		if e.Name == "MAFIN_URL" {
			gotMafin = e.Value
		}
	}
	if gotMafin != "https://coreo-bar.mafin.finforce.dev" {
		t.Fatalf("MAFIN_URL: got %q", gotMafin)
	}

	// Ingress host follows `webtop-<suffix>.mafin.finforce.dev`.
	if got, want := ing.Spec.Rules[0].Host, "webtop-feat-foo.mafin.finforce.dev"; got != want {
		t.Fatalf("ingress host: got %q, want %q", got, want)
	}

	// managed-by label is on every resource and on the pod template.
	for name, m := range map[string]map[string]string{
		"deployment":          dep.Labels,
		"service":             svc.Labels,
		"ingress":             ing.Labels,
		"deployment.template": dep.Spec.Template.Labels,
	} {
		if v := m[KitchenLabelKey]; v != KitchenLabelValue {
			t.Fatalf("%s missing managed-by label: %+v", name, m)
		}
	}

	// Provenance annotations live on the Deployment.
	a := dep.Annotations
	if a[KitchenAnnoBranch] != "feat/foo" {
		t.Fatalf("branch annotation: %q", a[KitchenAnnoBranch])
	}
	if a[KitchenAnnoImageTag] != "feat-foo" {
		t.Fatalf("image-tag annotation: %q", a[KitchenAnnoImageTag])
	}
	if a[KitchenAnnoCoreoURL] != "https://coreo-bar.mafin.finforce.dev" {
		t.Fatalf("coreo-backend annotation: %q", a[KitchenAnnoCoreoURL])
	}
	if a[KitchenAnnoCreatedBy] != "alice" {
		t.Fatalf("created-by annotation: %q", a[KitchenAnnoCreatedBy])
	}
	if _, err := time.Parse(time.RFC3339, a[KitchenAnnoCreatedAt]); err != nil {
		t.Fatalf("created-at annotation not RFC3339: %q (%v)", a[KitchenAnnoCreatedAt], err)
	}
}

func TestRenderWebtopManifest_DifferentBranchAndTag(t *testing.T) {
	// The branch picker may pass a branch ref (with slashes) while the
	// image tag must already be the slugified form — these can differ if
	// the user picks "main" as the build but names the deployment something
	// else.
	dep, _, _, err := renderWebtopManifest(WebtopDeploySpec{
		Suffix:   "my-test",
		ImageTag: "main",
		CoreoURL: "https://coreo.mafin.finforce.dev",
		Branch:   "main",
		Template: []byte(webtopTemplate),
	})
	if err != nil {
		t.Fatalf("renderWebtopManifest: %v", err)
	}
	if got, want := dep.Name, "mafin-coreo-app-my-test"; got != want {
		t.Fatalf("deployment name: got %q, want %q", got, want)
	}
	img := dep.Spec.Template.Spec.Containers[0].Image
	if got, want := img, "ghcr.io/finforce/mafin-coreo-app:main"; got != want {
		t.Fatalf("image: got %q, want %q", got, want)
	}
}

func TestRenderWebtopManifest_UnsubstitutedPlaceholderIsAnError(t *testing.T) {
	// A template that uses a placeholder kitchen doesn't know about should
	// fail loudly, not get applied with a literal `${MYSTERY}` in it.
	bogus := webtopTemplate + "\n# extra: ${MYSTERY}\n"
	_, _, _, err := renderWebtopManifest(WebtopDeploySpec{
		Suffix:   "x",
		ImageTag: "main",
		CoreoURL: "https://coreo.mafin.finforce.dev",
		Template: []byte(bogus),
	})
	if err == nil || !strings.Contains(err.Error(), "${MYSTERY}") {
		t.Fatalf("expected error mentioning ${MYSTERY}, got %v", err)
	}
}

func TestValidateSuffix(t *testing.T) {
	good := []string{"a", "main", "feat-foo", "abc-123", "x" + strings.Repeat("a", 44)}
	for _, s := range good {
		if err := validateSuffix(s); err != nil {
			t.Errorf("validateSuffix(%q) = %v, want nil", s, err)
		}
	}
	bad := []string{
		"",                          // empty
		"-foo",                      // starts with dash
		"Foo",                       // uppercase
		"feat/foo",                  // slash
		"foo bar",                   // space
		strings.Repeat("a", 46),     // too long
		"a" + strings.Repeat("b", 45), // 46 chars
	}
	for _, s := range bad {
		if err := validateSuffix(s); err == nil {
			t.Errorf("validateSuffix(%q) = nil, want error", s)
		}
	}
}
