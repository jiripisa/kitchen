package k8s

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// Kitchen marks the deployments it creates with this label so `kitchen webtop
// undeploy` can find them again without ever touching upstream review-app
// deployments.
const (
	KitchenLabelKey   = "app.kubernetes.io/managed-by"
	KitchenLabelValue = "kitchen"

	// Annotations carried on every kitchen-created resource. The deployer
	// reads these back to render the undeploy picker.
	KitchenAnnoBranch    = "kitchen.finforce.dev/branch"
	KitchenAnnoImageTag  = "kitchen.finforce.dev/image-tag"
	KitchenAnnoCoreoURL  = "kitchen.finforce.dev/coreo-backend"
	KitchenAnnoCreatedBy = "kitchen.finforce.dev/created-by"
	KitchenAnnoCreatedAt = "kitchen.finforce.dev/created-at"
)

// WebtopDeploySpec is the input for DeployWebtop — everything the wizard
// collected from the user, plus the raw YAML template fetched from upstream.
type WebtopDeploySpec struct {
	// Suffix is the slug used in the deployment / service / ingress name and
	// in the ingress host (`webtop-<suffix>.mafin.finforce.dev`).
	Suffix string

	// ImageTag is the tag of the ghcr.io/finforce/mafin-coreo-app image to
	// run. Comes from `EFFECTIVE_SLUG(branch)`; defaults to "main".
	ImageTag string

	// CoreoURL becomes the `MAFIN_URL` env var.
	CoreoURL string

	// Branch is the human-readable git ref the deployment was spawned from
	// (e.g. "feat/foo"). Recorded for the undeploy picker only.
	Branch string

	// CreatedBy is the user the deploy is attributed to (typically the
	// GitHub login of the current `gh` session). Empty when unknown.
	CreatedBy string

	// Template is the raw multi-document `k8s.yml` fetched from
	// `finforce/mafin-coreo-app@main`. We do the placeholder substitution
	// + label / annotation injection on this.
	Template []byte
}

// KitchenWebtop is one kitchen-managed webtop deployment as surfaced to the
// undeploy picker.
type KitchenWebtop struct {
	Namespace string
	Name      string
	ImageTag  string
	Branch    string
	CoreoURL  string
	CreatedBy string
	CreatedAt time.Time
}

// DeployWebtop renders the upstream `k8s.yml` template for the given spec,
// injects kitchen's managed-by label + provenance annotations, and creates
// the Deployment / Service / Ingress trio in `mafin`. Returns the rendered
// deployment name so the caller can show "deployed: <name>".
//
// Caller is expected to have ensured the trio doesn't exist yet — Deploy
// fails fast with a typed AlreadyExists error otherwise.
func (c *Client) DeployWebtop(ctx context.Context, spec WebtopDeploySpec) (string, error) {
	if err := validateSuffix(spec.Suffix); err != nil {
		return "", err
	}
	if !strings.HasPrefix(spec.CoreoURL, "http://") && !strings.HasPrefix(spec.CoreoURL, "https://") {
		return "", fmt.Errorf("coreo URL must start with http:// or https:// (got %q)", spec.CoreoURL)
	}

	dep, svc, ing, err := renderWebtopManifest(spec)
	if err != nil {
		return "", err
	}

	ns := dep.Namespace
	if ns == "" {
		ns = "mafin"
	}

	if _, err := c.cs.AppsV1().Deployments(ns).Create(ctx, dep, metav1.CreateOptions{}); err != nil {
		return "", fmt.Errorf("create deployment %s/%s: %w", ns, dep.Name, err)
	}
	if _, err := c.cs.CoreV1().Services(ns).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
		// Best-effort cleanup so a partial deploy doesn't leave orphan
		// resources around.
		_ = c.cs.AppsV1().Deployments(ns).Delete(ctx, dep.Name, metav1.DeleteOptions{})
		return "", fmt.Errorf("create service %s/%s: %w", ns, svc.Name, err)
	}
	if _, err := c.cs.NetworkingV1().Ingresses(ns).Create(ctx, ing, metav1.CreateOptions{}); err != nil {
		_ = c.cs.CoreV1().Services(ns).Delete(ctx, svc.Name, metav1.DeleteOptions{})
		_ = c.cs.AppsV1().Deployments(ns).Delete(ctx, dep.Name, metav1.DeleteOptions{})
		return "", fmt.Errorf("create ingress %s/%s: %w", ns, ing.Name, err)
	}
	return dep.Name, nil
}

// WebtopNameExists reports whether a Deployment with the given name already
// exists in `mafin`. The wizard uses this for the collision pre-check.
func (c *Client) WebtopNameExists(ctx context.Context, suffix string) (bool, error) {
	name := "mafin-coreo-app-" + suffix
	_, err := c.cs.AppsV1().Deployments("mafin").Get(ctx, name, metav1.GetOptions{})
	switch {
	case err == nil:
		return true, nil
	case apierrors.IsNotFound(err):
		return false, nil
	default:
		return false, fmt.Errorf("check %s: %w", name, err)
	}
}

// ListKitchenWebtops returns every webtop Deployment in `mafin` that carries
// the kitchen managed-by label, in alphabetical name order.
func (c *Client) ListKitchenWebtops(ctx context.Context) ([]KitchenWebtop, error) {
	selector := KitchenLabelKey + "=" + KitchenLabelValue
	list, err := c.cs.AppsV1().Deployments("mafin").List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return nil, fmt.Errorf("list kitchen webtops: %w", err)
	}
	out := make([]KitchenWebtop, 0, len(list.Items))
	for _, d := range list.Items {
		// Defence in depth: only surface true webtops (image match), even
		// though the label should already gate them.
		isWebtop := false
		for _, c := range d.Spec.Template.Spec.Containers {
			if strings.HasPrefix(c.Image, "ghcr.io/finforce/mafin-coreo-app:") ||
				strings.HasPrefix(c.Image, "ghcr.io/finforce/mafin-coreo-app@") ||
				c.Image == "ghcr.io/finforce/mafin-coreo-app" {
				isWebtop = true
				break
			}
		}
		if !isWebtop {
			continue
		}
		w := KitchenWebtop{
			Namespace: d.Namespace,
			Name:      d.Name,
			Branch:    d.Annotations[KitchenAnnoBranch],
			ImageTag:  d.Annotations[KitchenAnnoImageTag],
			CoreoURL:  d.Annotations[KitchenAnnoCoreoURL],
			CreatedBy: d.Annotations[KitchenAnnoCreatedBy],
		}
		if ts := d.Annotations[KitchenAnnoCreatedAt]; ts != "" {
			if t, err := time.Parse(time.RFC3339, ts); err == nil {
				w.CreatedAt = t
			}
		}
		out = append(out, w)
	}
	return out, nil
}

// DeleteWebtop removes the Deployment / Service / Ingress trio identified by
// (namespace, name). Missing resources are silently tolerated so a partially
// failed earlier deploy can still be cleaned up.
func (c *Client) DeleteWebtop(ctx context.Context, namespace, name string) error {
	var firstErr error
	tryDelete := func(kind string, fn func() error) {
		if err := fn(); err != nil && !apierrors.IsNotFound(err) {
			if firstErr == nil {
				firstErr = fmt.Errorf("delete %s %s/%s: %w", kind, namespace, name, err)
			}
		}
	}
	tryDelete("ingress", func() error {
		return c.cs.NetworkingV1().Ingresses(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	})
	tryDelete("service", func() error {
		return c.cs.CoreV1().Services(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	})
	tryDelete("deployment", func() error {
		return c.cs.AppsV1().Deployments(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	})
	return firstErr
}

// validateSuffix mirrors the regex enforced by deploy-custom and by the CI
// slug logic: starts with [a-z0-9], at most 45 chars, only [a-z0-9-] inside.
func validateSuffix(s string) error {
	if s == "" {
		return fmt.Errorf("suffix must not be empty")
	}
	if len(s) > 45 {
		return fmt.Errorf("suffix must be 45 chars or fewer (got %d)", len(s))
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= '0' && r <= '9':
			// ok always
		case r == '-':
			if i == 0 {
				return fmt.Errorf("suffix must not start with a dash")
			}
		default:
			return fmt.Errorf("suffix must match [a-z0-9][a-z0-9-]{0,44} (offending char %q at %d)", string(r), i)
		}
	}
	return nil
}

// renderWebtopManifest applies the same substitution sequence as
// `script/deploy-custom`, then unmarshals the three docs into typed objects
// and injects kitchen's label + annotations.
func renderWebtopManifest(spec WebtopDeploySpec) (*appsv1.Deployment, *corev1.Service, *networkingv1.Ingress, error) {
	if len(spec.Template) == 0 {
		return nil, nil, nil, fmt.Errorf("empty webtop template")
	}

	// 1) Decouple the image tag from SUFFIX — the upstream template has
	//    `image: ghcr.io/finforce/mafin-coreo-app:${SUFFIX}` and `deploy-custom`
	//    `sed`s `${SUFFIX}` → `${IMAGE_TAG}` only on the image line, so the
	//    deployment name keeps the suffix while the image gets its own tag.
	rendered := strings.ReplaceAll(string(spec.Template),
		"mafin-coreo-app:${SUFFIX}",
		"mafin-coreo-app:${IMAGE_TAG}")

	// 2) Substitute the four placeholders (SUFFIX, IMAGE_TAG, COREO_URL,
	//    NONCE). We do it with strings.ReplaceAll rather than full envsubst
	//    so a stray `$variable` in MAFIN_TOKEN can never accidentally
	//    resolve to anything.
	nonce := fmt.Sprintf("%d", time.Now().UnixNano())
	imageTag := spec.ImageTag
	if imageTag == "" {
		imageTag = "main"
	}
	repl := map[string]string{
		"${SUFFIX}":    spec.Suffix,
		"${IMAGE_TAG}": imageTag,
		"${COREO_URL}": spec.CoreoURL,
		"${NONCE}":     nonce,
	}
	for k, v := range repl {
		rendered = strings.ReplaceAll(rendered, k, v)
	}
	if i := strings.Index(rendered, "${"); i >= 0 {
		// Look for any unsubstituted `${...}` token so the caller can fix
		// the template (or kitchen's substitution list) rather than apply
		// a manifest with literal `${...}` in it.
		end := strings.Index(rendered[i:], "}")
		if end > 0 {
			return nil, nil, nil, fmt.Errorf("template has unsubstituted placeholder %q", rendered[i:i+end+1])
		}
	}

	docs := splitYAMLDocs([]byte(rendered))
	if len(docs) < 3 {
		return nil, nil, nil, fmt.Errorf("expected 3 yaml documents in template, got %d", len(docs))
	}

	var dep *appsv1.Deployment
	var svc *corev1.Service
	var ing *networkingv1.Ingress

	for _, doc := range docs {
		kind, err := peekKind(doc)
		if err != nil {
			return nil, nil, nil, err
		}
		switch kind {
		case "Deployment":
			dep = &appsv1.Deployment{}
			if err := yaml.Unmarshal(doc, dep); err != nil {
				return nil, nil, nil, fmt.Errorf("unmarshal Deployment: %w", err)
			}
		case "Service":
			svc = &corev1.Service{}
			if err := yaml.Unmarshal(doc, svc); err != nil {
				return nil, nil, nil, fmt.Errorf("unmarshal Service: %w", err)
			}
		case "Ingress":
			ing = &networkingv1.Ingress{}
			if err := yaml.Unmarshal(doc, ing); err != nil {
				return nil, nil, nil, fmt.Errorf("unmarshal Ingress: %w", err)
			}
		}
	}
	if dep == nil || svc == nil || ing == nil {
		return nil, nil, nil, fmt.Errorf("template missing one of Deployment/Service/Ingress")
	}

	// Inject kitchen's managed-by label everywhere it can be selected on,
	// plus the provenance annotations on the Deployment.
	addLabel(&dep.ObjectMeta, KitchenLabelKey, KitchenLabelValue)
	addLabel(&svc.ObjectMeta, KitchenLabelKey, KitchenLabelValue)
	addLabel(&ing.ObjectMeta, KitchenLabelKey, KitchenLabelValue)
	if dep.Spec.Template.Labels == nil {
		dep.Spec.Template.Labels = map[string]string{}
	}
	dep.Spec.Template.Labels[KitchenLabelKey] = KitchenLabelValue

	annos := map[string]string{
		KitchenAnnoBranch:    spec.Branch,
		KitchenAnnoImageTag:  imageTag,
		KitchenAnnoCoreoURL:  spec.CoreoURL,
		KitchenAnnoCreatedBy: spec.CreatedBy,
		KitchenAnnoCreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	for k, v := range annos {
		if v == "" {
			continue
		}
		addAnnotation(&dep.ObjectMeta, k, v)
	}

	// Strip server-side managed fields on the off chance the template was
	// pulled from a `kubectl get` output instead of the repo. Belt + braces.
	dep.ManagedFields = nil
	svc.ManagedFields = nil
	ing.ManagedFields = nil

	return dep, svc, ing, nil
}

func addLabel(meta *metav1.ObjectMeta, key, value string) {
	if meta.Labels == nil {
		meta.Labels = map[string]string{}
	}
	meta.Labels[key] = value
}

func addAnnotation(meta *metav1.ObjectMeta, key, value string) {
	if meta.Annotations == nil {
		meta.Annotations = map[string]string{}
	}
	meta.Annotations[key] = value
}

// splitYAMLDocs splits a multi-document YAML stream on `---` lines.
// sigs.k8s.io/yaml's decoder can do this too via a YAMLReader, but the
// template here is tiny so a string-level split keeps the dependency
// surface smaller.
func splitYAMLDocs(in []byte) [][]byte {
	out := [][]byte{}
	chunks := bytes.Split(in, []byte("\n---"))
	for _, c := range chunks {
		c = bytes.TrimSpace(c)
		// Drop a leading "---" that follows a trimmed split.
		c = bytes.TrimPrefix(c, []byte("---"))
		c = bytes.TrimSpace(c)
		if len(c) == 0 {
			continue
		}
		out = append(out, c)
	}
	return out
}

// peekKind extracts the .kind field from a YAML document without fully
// unmarshalling it.
func peekKind(doc []byte) (string, error) {
	var stub struct {
		Kind string `json:"kind"`
	}
	if err := yaml.Unmarshal(doc, &stub); err != nil {
		return "", fmt.Errorf("peek kind: %w", err)
	}
	return stub.Kind, nil
}
