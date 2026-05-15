package k8s

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IngressEndpoint is a flattened view of one (host, service-backend) pair
// from an Ingress. One Ingress can expose multiple endpoints; we emit a
// row per (rule.host × path.backend.service.name) combination.
type IngressEndpoint struct {
	Namespace   string
	ServiceName string
	Host        string
}

// ListAllIngresses returns ingress endpoints across every namespace the user
// can list ingresses in. Rules without a host or a Service-typed backend
// are skipped.
func (c *Client) ListAllIngresses(ctx context.Context) ([]IngressEndpoint, error) {
	list, err := c.cs.NetworkingV1().Ingresses("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list ingresses: %w", err)
	}
	var out []IngressEndpoint
	for _, ing := range list.Items {
		for _, rule := range ing.Spec.Rules {
			if rule.Host == "" || rule.HTTP == nil {
				continue
			}
			for _, p := range rule.HTTP.Paths {
				if p.Backend.Service == nil {
					continue
				}
				out = append(out, IngressEndpoint{
					Namespace:   ing.Namespace,
					ServiceName: p.Backend.Service.Name,
					Host:        rule.Host,
				})
			}
		}
	}
	return out, nil
}
