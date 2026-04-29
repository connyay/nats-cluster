package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

// Local ports the harness listens on via `fly proxy`. Single source of truth —
// port mappings (main.go) and assertion URLs are derived from these.
const (
	localNATSPort    = 14222
	localMonitorPort = 18222
	localMetricsPort = 17777
)

// runAssertions runs all cluster assertions against the proxy endpoints.
// expectedNodes is the total count across all regions.
func runAssertions(ctx context.Context, expectedNodes int) error {
	monitor := fmt.Sprintf("http://127.0.0.1:%d", localMonitorPort)
	metrics := fmt.Sprintf("http://127.0.0.1:%d/metrics", localMetricsPort)
	natsURL := fmt.Sprintf("nats://127.0.0.1:%d", localNATSPort)

	if err := waitForVarz(ctx, monitor+"/varz", 90*time.Second); err != nil {
		return fmt.Errorf("varz never came up: %w", err)
	}

	if err := assertHealthz(monitor + "/healthz"); err != nil {
		return err
	}
	log.Print("  PASS /healthz")

	if err := assertClusterFormed(monitor+"/varz", expectedNodes); err != nil {
		return err
	}
	log.Print("  PASS cluster formed")

	if err := assertExporterMetrics(metrics); err != nil {
		return err
	}
	log.Print("  PASS exporter /metrics")

	if err := assertPubSub(ctx, natsURL); err != nil {
		return err
	}
	log.Print("  PASS pub/sub round-trip")

	if err := assertJetStream(ctx, natsURL, expectedNodes); err != nil {
		return err
	}
	log.Print("  PASS jetstream round-trip")

	return nil
}

func httpGetOK(url string, timeout time.Duration) ([]byte, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return body, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func waitForVarz(ctx context.Context, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		_, err := httpGetOK(url, 3*time.Second)
		if err == nil {
			return nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return fmt.Errorf("after %s: %w", timeout, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func assertHealthz(url string) error {
	if _, err := httpGetOK(url, 5*time.Second); err != nil {
		return fmt.Errorf("healthz: %w", err)
	}
	return nil
}

type varz struct {
	ServerName string `json:"server_name"`
	Version    string `json:"version"`
	Cluster    struct {
		Name string   `json:"name"`
		URLs []string `json:"urls"`
	} `json:"cluster"`
	// Routes is a count of active route connections in NATS 2.10+. With route
	// pooling enabled, an N-node cluster has multiple route connections per peer.
	// Per-route detail lives at /routez.
	Routes      int      `json:"routes"`
	ConnectURLs []string `json:"connect_urls"`
}

func assertClusterFormed(url string, expectedNodes int) error {
	body, err := httpGetOK(url, 5*time.Second)
	if err != nil {
		return fmt.Errorf("varz: %w", err)
	}
	var v varz
	if err := json.Unmarshal(body, &v); err != nil {
		return fmt.Errorf("varz parse: %w", err)
	}
	log.Printf("    nats version=%s server=%s routes=%d connect_urls=%d",
		v.Version, v.ServerName, v.Routes, len(v.ConnectURLs))

	// In a single-region cluster every peer is connected, so we expect at least
	// one route. Multi-region is the same from any one node's perspective.
	if expectedNodes > 1 && v.Routes < 1 {
		return fmt.Errorf("expected ≥1 cluster route, got %d (varz=%s)", v.Routes, string(body))
	}
	return nil
}

func assertExporterMetrics(url string) error {
	body, err := httpGetOK(url, 5*time.Second)
	if err != nil {
		return fmt.Errorf("metrics: %w", err)
	}
	text := string(body)
	// gnatsd_varz_* is what prometheus-nats-exporter emits when scraping /varz.
	// If this family disappears, the exporter or NATS broke its monitoring shape.
	if !strings.Contains(text, "gnatsd_varz_") {
		preview := text
		if len(preview) > 400 {
			preview = preview[:400] + "..."
		}
		return fmt.Errorf("metrics missing gnatsd_varz_* family (got %d bytes; preview: %s)", len(body), preview)
	}
	return nil
}

func natsConnect(ctx context.Context, url string) (*nats.Conn, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(60 * time.Second)
	}
	var lastErr error
	for {
		nc, err := nats.Connect(url,
			nats.Timeout(3*time.Second),
			nats.MaxReconnects(0),
			nats.RetryOnFailedConnect(false),
		)
		if err == nil {
			return nc, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("connect %s: %w", url, lastErr)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
}

func assertPubSub(ctx context.Context, url string) error {
	nc, err := natsConnect(ctx, url)
	if err != nil {
		return err
	}
	defer nc.Close()

	sub, err := nc.SubscribeSync("e2e.pubsub")
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	payload := []byte("hello-" + time.Now().Format(time.RFC3339Nano))
	if err := nc.Publish("e2e.pubsub", payload); err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	if err := nc.Flush(); err != nil {
		return fmt.Errorf("flush: %w", err)
	}
	msg, err := sub.NextMsg(5 * time.Second)
	if err != nil {
		return fmt.Errorf("recv: %w", err)
	}
	if string(msg.Data) != string(payload) {
		return fmt.Errorf("payload mismatch: got %q want %q", msg.Data, payload)
	}
	return nil
}

// waitForStreamLeader polls StreamInfo until the stream reports a RAFT leader
// (or, for R=1, simply exists). AddStream returns once the stream is created
// in meta, but the data-plane RAFT group may still be electing — publishing
// before then yields "no response from stream".
func waitForStreamLeader(ctx context.Context, js nats.JetStreamContext, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		info, err := js.StreamInfo(name)
		if err == nil {
			if info.Cluster == nil || info.Cluster.Leader != "" {
				return nil
			}
			lastErr = fmt.Errorf("no leader yet (replicas=%d)", len(info.Cluster.Replicas)+1)
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("stream %s leader not elected within %s: %w", name, timeout, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// waitForConsumerLeader polls ConsumerInfo on the subscription's consumer until
// it reports a RAFT leader. Mirrors waitForStreamLeader: PullSubscribe returns
// as soon as the consumer is created in meta, but its RAFT group may still be
// electing — Fetch before then yields "no responders available for request".
func waitForConsumerLeader(ctx context.Context, sub *nats.Subscription, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		info, err := sub.ConsumerInfo()
		if err == nil {
			if info.Cluster == nil || info.Cluster.Leader != "" {
				return nil
			}
			lastErr = fmt.Errorf("no leader yet (replicas=%d)", len(info.Cluster.Replicas)+1)
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("consumer leader not elected within %s: %w", timeout, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func assertJetStream(ctx context.Context, url string, nodes int) error {
	nc, err := natsConnect(ctx, url)
	if err != nil {
		return err
	}
	defer nc.Close()

	// MaxWait raises the per-request timeout for JS API calls (AddStream,
	// Publish, etc). The default 5s is tight when traffic flows through the Fly
	// proxy/WireGuard tunnel.
	js, err := nc.JetStream(nats.MaxWait(10 * time.Second))
	if err != nil {
		return fmt.Errorf("jetstream context: %w", err)
	}

	streamName := fmt.Sprintf("E2E_%d", time.Now().UnixNano())
	replicas := max(1, min(3, nodes))
	if _, err := js.AddStream(&nats.StreamConfig{
		Name:     streamName,
		Subjects: []string{streamName + ".>"},
		Replicas: replicas,
	}); err != nil {
		return fmt.Errorf("add stream R=%d: %w", replicas, err)
	}
	defer js.DeleteStream(streamName)

	// Wait for the stream's RAFT leader to be elected before publishing.
	// Without this, the first js.Publish can race ahead of leader election and
	// time out with "no response from stream", especially for R>1 over the proxy.
	if err := waitForStreamLeader(ctx, js, streamName, 30*time.Second); err != nil {
		return err
	}

	const N = 50
	for i := 0; i < N; i++ {
		if _, err := js.Publish(streamName+".x", []byte(fmt.Sprintf("msg-%d", i))); err != nil {
			return fmt.Errorf("publish %d: %w", i, err)
		}
	}

	// Pull subscribe (request/response) is more reliable over the WireGuard
	// proxy than push delivery to an ephemeral inbox.
	sub, err := js.PullSubscribe(streamName+".>", "")
	if err != nil {
		return fmt.Errorf("pull subscribe: %w", err)
	}
	defer sub.Unsubscribe()

	if err := waitForConsumerLeader(ctx, sub, 30*time.Second); err != nil {
		return err
	}

	received := 0
	deadline := time.Now().Add(30 * time.Second)
	for received < N {
		if time.Now().After(deadline) {
			return fmt.Errorf("only received %d/%d messages within 30s", received, N)
		}
		msgs, err := sub.Fetch(N-received, nats.MaxWait(10*time.Second))
		if err != nil && !errors.Is(err, nats.ErrTimeout) {
			return fmt.Errorf("fetch: %w", err)
		}
		for _, msg := range msgs {
			want := fmt.Sprintf("msg-%d", received)
			if string(msg.Data) != want {
				return fmt.Errorf("msg %d mismatch: got %q want %q", received, msg.Data, want)
			}
			_ = msg.Ack()
			received++
		}
	}
	return nil
}
