// Package k8s wraps the bits of client-go that kitchen actually uses.
//
// It returns plain Go types so the TUI layer never has to import client-go.
package k8s

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	"sigs.k8s.io/yaml"
)

// client-go has very conservative default rate limits (QPS=5, Burst=10)
// that throttle our parallel fan-out across many deployments and produce
// noisy "Waited before sending request" log lines. Bump them to values
// reasonable for an interactive CLI tool.
const (
	clientQPS   = 50
	clientBurst = 100
)

func init() {
	// Silence klog. client-go uses it to surface internal warnings (rate
	// limiting, retry hints, ...) that would otherwise interleave with
	// our table output.
	klog.SetLogger(logr.Discard())
}

// Client is a thin, opinionated wrapper around client-go.
type Client struct {
	cs      *kubernetes.Clientset
	context string
}

// NewClient builds a client using $KUBECONFIG (strict — must exist) or the
// default precedence (~/.kube/config, best-effort). Returns a friendly error
// when no config is reachable so the user knows what to do.
func NewClient() (*Client, error) {
	loading := clientcmd.NewDefaultClientConfigLoadingRules()
	// Only force a single path when the user explicitly set $KUBECONFIG.
	// Otherwise let the default precedence rules look at ~/.kube/config
	// without erroring when it's missing — we'll surface a friendlier
	// message below.
	if p := os.Getenv("KUBECONFIG"); p != "" {
		loading.ExplicitPath = p
	}

	cfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loading,
		&clientcmd.ConfigOverrides{},
	)

	raw, err := cfg.RawConfig()
	if err != nil {
		return nil, fmt.Errorf("read kubeconfig (%s): %w", kubeconfigHint(), err)
	}
	if len(raw.Contexts) == 0 || raw.CurrentContext == "" {
		return nil, fmt.Errorf("no kubeconfig found at %s — set $KUBECONFIG or create one (e.g. via `kubectl config`)", kubeconfigHint())
	}

	restCfg, err := cfg.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("build rest config: %w", err)
	}
	restCfg.QPS = clientQPS
	restCfg.Burst = clientBurst

	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("build clientset: %w", err)
	}

	return &Client{cs: cs, context: raw.CurrentContext}, nil
}

// kubeconfigHint returns the path the user would expect us to read from, for
// inclusion in error messages.
func kubeconfigHint() string {
	if p := os.Getenv("KUBECONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "~/.kube/config"
	}
	return filepath.Join(home, ".kube", "config")
}

// Context returns the current kubeconfig context name.
func (c *Client) Context() string { return c.context }

// ListNamespaces returns all namespace names sorted alphabetically.
func (c *Client) ListNamespaces(ctx context.Context) ([]string, error) {
	list, err := c.cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}
	names := make([]string, 0, len(list.Items))
	for _, ns := range list.Items {
		names = append(names, ns.Name)
	}
	sort.Strings(names)
	return names, nil
}

// Deployment is the trimmed-down view of a Deployment that the TUI cares about.
type Deployment struct {
	Name       string
	Namespace  string
	Selector   map[string]string
	Replicas   int32
	Ready      int32
	Containers []Container
}

// Container is the subset of a pod-spec container we expose — enough to
// identify what application a Deployment is running and to read its
// literal environment values.
type Container struct {
	Name  string
	Image string
	// Env holds direct (literal) env values from the pod spec. Entries
	// sourced from ConfigMaps, Secrets or fieldRefs (valueFrom) are skipped
	// because resolving them requires extra API calls per deployment.
	Env map[string]string
}

// ListDeployments returns all deployments in a namespace.
func (c *Client) ListDeployments(ctx context.Context, namespace string) ([]Deployment, error) {
	list, err := c.cs.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list deployments in %s: %w", namespace, err)
	}
	out := make([]Deployment, 0, len(list.Items))
	for _, d := range list.Items {
		out = append(out, toDeployment(d))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// GetDeploymentYAML fetches a Deployment and returns it as a YAML document.
// Server-side noise (managedFields) is stripped, and the TypeMeta is
// repopulated so the output reads like a `kubectl get -o yaml`.
func (c *Client) GetDeploymentYAML(ctx context.Context, namespace, name string) (string, error) {
	dep, err := c.cs.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get deployment %s/%s: %w", namespace, name, err)
	}
	dep.TypeMeta.APIVersion = "apps/v1"
	dep.TypeMeta.Kind = "Deployment"
	dep.ManagedFields = nil
	b, err := yaml.Marshal(dep)
	if err != nil {
		return "", fmt.Errorf("marshal deployment yaml: %w", err)
	}
	return string(b), nil
}

// ListAllDeployments returns deployments across every namespace the user has
// list access to, sorted by (namespace, name). client-go treats an empty
// namespace string as "all namespaces".
func (c *Client) ListAllDeployments(ctx context.Context) ([]Deployment, error) {
	list, err := c.cs.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list deployments across all namespaces: %w", err)
	}
	out := make([]Deployment, 0, len(list.Items))
	for _, d := range list.Items {
		out = append(out, toDeployment(d))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func toDeployment(d appsv1.Deployment) Deployment {
	sel := map[string]string{}
	if d.Spec.Selector != nil {
		for k, v := range d.Spec.Selector.MatchLabels {
			sel[k] = v
		}
	}
	var replicas int32
	if d.Spec.Replicas != nil {
		replicas = *d.Spec.Replicas
	}
	containers := make([]Container, 0, len(d.Spec.Template.Spec.Containers))
	for _, c := range d.Spec.Template.Spec.Containers {
		env := map[string]string{}
		for _, e := range c.Env {
			if e.ValueFrom == nil {
				env[e.Name] = e.Value
			}
		}
		containers = append(containers, Container{Name: c.Name, Image: c.Image, Env: env})
	}
	return Deployment{
		Name:       d.Name,
		Namespace:  d.Namespace,
		Selector:   sel,
		Replicas:   replicas,
		Ready:      d.Status.ReadyReplicas,
		Containers: containers,
	}
}

// ListPodsForDeployment returns the names of the running pods that match a
// deployment's label selector. The deployment is re-read to get the latest
// selector each time.
func (c *Client) ListPodsForDeployment(ctx context.Context, namespace, name string) ([]string, error) {
	dep, err := c.cs.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get deployment %s/%s: %w", namespace, name, err)
	}
	if dep.Spec.Selector == nil {
		return nil, nil
	}
	sel := labels.SelectorFromSet(dep.Spec.Selector.MatchLabels).String()

	pods, err := c.cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: sel})
	if err != nil {
		return nil, fmt.Errorf("list pods for %s/%s: %w", namespace, name, err)
	}
	names := make([]string, 0, len(pods.Items))
	for _, p := range pods.Items {
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		names = append(names, p.Name)
	}
	sort.Strings(names)
	return names, nil
}
