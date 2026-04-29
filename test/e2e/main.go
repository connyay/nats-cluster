// e2e runs an end-to-end test of the nats-cluster image against a real Fly.io app.
//
// Usage:
//
//	go run ./test/e2e [flags]
//
// Flags:
//
//	-regions     comma-separated fly regions (default "iad")
//	-count       machines per region (default 3)
//	-app-prefix  prefix for the throwaway app name (default "nats-cluster-test")
//	-org         fly organization (default "personal")
//	-keep        leave the app alive on exit (for debugging failures)
//	-timeout     overall test timeout (default 15m)
//	-image       deploy this pre-built image instead of building locally
//
// Prereqs: `fly auth login`, Docker running locally (for --local-only build).
//
// The harness creates a unique throwaway app, deploys the locally-built image,
// scales to the requested count, runs assertions over `fly proxy`, and tears
// the app down on the way out (including on Ctrl-C and on assertion failure).
// Exit code is 0 on PASS, non-zero on FAIL.
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"text/template"
	"time"
	"unicode"
)

//go:embed fly.toml.tmpl
var tmplFS embed.FS

var (
	flagRegions   = flag.String("regions", "iad", "comma-separated fly regions")
	flagCount     = flag.Int("count", 3, "machines per region")
	flagAppPrefix = flag.String("app-prefix", "nats-cluster-test", "prefix for the throwaway app")
	flagOrg       = flag.String("org", "personal", "fly organization")
	flagKeep      = flag.Bool("keep", false, "don't destroy the app on exit (for debugging)")
	flagTimeout   = flag.Duration("timeout", 15*time.Minute, "overall test timeout")
	flagImage     = flag.String("image", "", "deploy this image (skip local build)")
)

func main() {
	flag.Parse()
	log.SetFlags(log.Ltime)

	regions := strings.FieldsFunc(*flagRegions, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
	if len(regions) == 0 {
		log.Fatal("at least one region required (-regions)")
	}

	if err := chdirToRepoRoot(); err != nil {
		log.Fatalf("could not find repo root: %v", err)
	}

	app := fmt.Sprintf("%s-%d", *flagAppPrefix, time.Now().Unix())

	ctx, cancel := context.WithTimeout(context.Background(), *flagTimeout)
	defer cancel()

	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			if *flagKeep {
				log.Printf("--keep set; app %s left alive — destroy with `fly apps destroy -y %s`", app, app)
				return
			}
			log.Printf("destroying %s ...", app)
			dctx, dcancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer dcancel()
			if err := flyRun(dctx, "apps", "destroy", "-y", app); err != nil {
				log.Printf("destroy failed: %v — try `fly apps destroy -y %s` manually", err, app)
			}
		})
	}
	defer cleanup()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Print("interrupted, cleaning up ...")
		cleanup()
		os.Exit(130)
	}()

	if err := run(ctx, app, regions); err != nil {
		log.Printf("FAIL: %v", err)
		dumpDiagnostics(app)
		cleanup()
		os.Exit(1)
	}
	log.Print("PASS")
}

func run(ctx context.Context, app string, regions []string) error {
	primary := regions[0]
	expected := *flagCount * len(regions)

	log.Printf("creating app %s (org=%s) ...", app, *flagOrg)
	if err := flyRun(ctx, "apps", "create", app, "--org", *flagOrg); err != nil {
		return fmt.Errorf("apps create: %w", err)
	}

	tomlPath, err := writeFlyToml(app, primary)
	if err != nil {
		return fmt.Errorf("render fly.toml: %w", err)
	}
	defer os.Remove(tomlPath)

	deployArgs := []string{
		"deploy",
		"--config", tomlPath,
		"--app", app,
		"--ha=false",
		// A single-node cluster never passes /healthz — JetStream blocks on
		// "Waiting for routing to be established" until peers exist. Skip the
		// post-deploy health wait; we scale to N+ and gate on our own poll.
		"--strategy", "immediate",
	}
	if *flagImage != "" {
		deployArgs = append(deployArgs, "--image", *flagImage)
	} else {
		deployArgs = append(deployArgs, "--local-only")
	}
	log.Printf("deploying ...")
	if err := flyRun(ctx, deployArgs...); err != nil {
		return fmt.Errorf("deploy: %w", err)
	}

	for _, r := range regions {
		log.Printf("scaling region %s to %d ...", r, *flagCount)
		if err := flyRun(ctx,
			"scale", "count", fmt.Sprintf("%d", *flagCount),
			"--region", r,
			"--app", app,
			"--config", tomlPath,
			"--yes",
		); err != nil {
			return fmt.Errorf("scale %s: %w", r, err)
		}
	}

	log.Printf("waiting for %d machines healthy (up to 5m) ...", expected)
	if err := waitMachinesHealthy(ctx, app, expected, 5*time.Minute); err != nil {
		return fmt.Errorf("wait healthy: %w", err)
	}

	log.Printf("starting fly proxies: %d→4222, %d→8222, %d→7777 ...",
		localNATSPort, localMonitorPort, localMetricsPort)
	proxies, err := startProxies(app, []string{
		fmt.Sprintf("%d:4222", localNATSPort),
		fmt.Sprintf("%d:8222", localMonitorPort),
		fmt.Sprintf("%d:7777", localMetricsPort),
	})
	if err != nil {
		return fmt.Errorf("start proxies: %w", err)
	}
	defer proxies.stop()

	log.Print("running assertions ...")
	return runAssertions(ctx, expected)
}

func writeFlyToml(app, primary string) (string, error) {
	tmpl, err := template.ParseFS(tmplFS, "fly.toml.tmpl")
	if err != nil {
		return "", err
	}
	// Render at repo root so [build] dockerfile = "Dockerfile" resolves correctly.
	f, err := os.CreateTemp(".", "fly.test.*.toml")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := tmpl.Execute(f, map[string]string{
		"AppName":       app,
		"PrimaryRegion": primary,
	}); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// chdirToRepoRoot walks up from cwd until it finds a directory containing
// both Dockerfile and go.mod (the project root), then chdirs there.
func chdirToRepoRoot() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	for {
		_, dErr := os.Stat(filepath.Join(dir, "Dockerfile"))
		_, gErr := os.Stat(filepath.Join(dir, "go.mod"))
		if dErr == nil && gErr == nil {
			return os.Chdir(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return errors.New("walked to fs root without finding Dockerfile + go.mod")
		}
		dir = parent
	}
}

func dumpDiagnostics(app string) {
	log.Print("--- fly status ---")
	sctx, sc := context.WithTimeout(context.Background(), 15*time.Second)
	defer sc()
	_ = flyRun(sctx, "status", "--app", app)
	log.Print("--- fly logs (10s tail) ---")
	lctx, lc := context.WithTimeout(context.Background(), 10*time.Second)
	defer lc()
	_ = flyRun(lctx, "logs", "--app", app)
}
