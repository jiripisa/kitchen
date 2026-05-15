package k8s

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// LogLine is one line produced by one pod.
type LogLine struct {
	Pod  string
	Line string
}

// LastLogTimeForPod returns the timestamp of the most recent log line
// emitted by the named pod, or a zero time when the pod hasn't logged
// anything yet (or the read fails).
func (c *Client) LastLogTimeForPod(ctx context.Context, namespace, pod string) (time.Time, error) {
	tail := int64(1)
	opts := &corev1.PodLogOptions{
		TailLines:  &tail,
		Timestamps: true,
	}
	rc, err := c.cs.CoreV1().Pods(namespace).GetLogs(pod, opts).Stream(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("open log stream for %s: %w", pod, err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return time.Time{}, fmt.Errorf("read log for %s: %w", pod, err)
	}
	line := strings.TrimRight(string(data), "\n\r")
	if line == "" {
		return time.Time{}, nil // pod hasn't logged anything yet
	}
	// With Timestamps:true, every line starts with an RFC3339Nano timestamp
	// followed by a single space and the original message.
	ts, _, ok := strings.Cut(line, " ")
	if !ok {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse log timestamp %q: %w", ts, err)
	}
	return t, nil
}

// LastLogTimeForDeployment picks the first pod of a deployment and returns
// its last log line's timestamp. Zero time when the deployment has no pods
// or the pod hasn't logged anything yet.
func (c *Client) LastLogTimeForDeployment(ctx context.Context, namespace, name string) (time.Time, error) {
	pods, err := c.ListPodsForDeployment(ctx, namespace, name)
	if err != nil {
		return time.Time{}, err
	}
	if len(pods) == 0 {
		return time.Time{}, nil
	}
	return c.LastLogTimeForPod(ctx, namespace, pods[0])
}

// LogStream fans logs from every pod of a deployment into a single channel.
type LogStream struct {
	Lines  <-chan LogLine
	Errors <-chan error

	cancel context.CancelFunc
	wg     *sync.WaitGroup
	closed chan struct{}
}

// Close stops every underlying log reader. Safe to call more than once.
func (s *LogStream) Close() {
	if s == nil {
		return
	}
	s.cancel()
	// Drain channels in the background — we don't want Close to block on the
	// consumer.
	go func() {
		s.wg.Wait()
		close(s.closed)
	}()
}

// StreamDeploymentLogs starts following logs for every pod of a deployment
// concurrently. The caller MUST call Close to release resources.
//
// If pods come and go during the stream, the caller can call this again with a
// fresh pod list — the simpler "one stream per session" model keeps the code
// readable, and `kitchen log` is interactive enough that re-running is fine.
func (c *Client) StreamDeploymentLogs(ctx context.Context, namespace string, pods []string, tailLines int64) (*LogStream, error) {
	if len(pods) == 0 {
		return nil, fmt.Errorf("no pods to stream")
	}

	ctx, cancel := context.WithCancel(ctx)
	lines := make(chan LogLine, 256)
	errs := make(chan error, len(pods))
	var wg sync.WaitGroup

	for _, pod := range pods {
		wg.Add(1)
		go func(podName string) {
			defer wg.Done()
			if err := c.streamPod(ctx, namespace, podName, tailLines, lines); err != nil {
				if ctx.Err() == nil { // ignore cancellation errors
					errs <- fmt.Errorf("pod %s: %w", podName, err)
				}
			}
		}(pod)
	}

	stream := &LogStream{
		Lines:  lines,
		Errors: errs,
		cancel: cancel,
		wg:     &wg,
		closed: make(chan struct{}),
	}

	// Close the fan-in channels once every reader has exited.
	go func() {
		wg.Wait()
		close(lines)
		close(errs)
	}()

	return stream, nil
}

func (c *Client) streamPod(ctx context.Context, namespace, pod string, tail int64, out chan<- LogLine) error {
	opts := &corev1.PodLogOptions{
		Follow:    true,
		TailLines: &tail,
	}
	req := c.cs.CoreV1().Pods(namespace).GetLogs(pod, opts)
	rc, err := req.Stream(ctx)
	if err != nil {
		return fmt.Errorf("open log stream: %w", err)
	}
	defer rc.Close()

	r := bufio.NewReader(rc)
	for {
		line, err := r.ReadString('\n')
		if line != "" {
			// Strip trailing newline for cleaner rendering.
			n := len(line)
			if n > 0 && line[n-1] == '\n' {
				line = line[:n-1]
			}
			select {
			case <-ctx.Done():
				return nil
			case out <- LogLine{Pod: pod, Line: line}:
			}
		}
		if err != nil {
			if err == io.EOF || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("read: %w", err)
		}
	}
}
