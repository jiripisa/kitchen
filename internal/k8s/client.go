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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Client is a thin, opinionated wrapper around client-go.
type Client struct {
	cs      *kubernetes.Clientset
	context string
}

// NewClient builds a client from $KUBECONFIG (falling back to ~/.kube/config)
// and returns the current context name alongside the client.
func NewClient() (*Client, error) {
	loading := clientcmd.NewDefaultClientConfigLoadingRules()
	loading.ExplicitPath = kubeconfigPath()

	cfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loading,
		&clientcmd.ConfigOverrides{},
	)

	raw, err := cfg.RawConfig()
	if err != nil {
		return nil, fmt.Errorf("read kubeconfig: %w", err)
	}

	restCfg, err := cfg.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("build rest config: %w", err)
	}

	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("build clientset: %w", err)
	}

	return &Client{cs: cs, context: raw.CurrentContext}, nil
}

func kubeconfigPath() string {
	if p := os.Getenv("KUBECONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
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
	Name      string
	Namespace string
	Selector  map[string]string
	Replicas  int32
	Ready     int32
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
	return Deployment{
		Name:      d.Name,
		Namespace: d.Namespace,
		Selector:  sel,
		Replicas:  replicas,
		Ready:     d.Status.ReadyReplicas,
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
