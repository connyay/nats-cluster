package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sort"
	"syscall"
	"text/template"
	"time"

	"github.com/jeffh/nats-cluster/pkg/privnet"
	"github.com/jeffh/nats-cluster/pkg/supervisor"

	_ "embed"
)

func main() {
	natsVars, err := initNatsConfig()
	if err != nil {
		slog.Error("failed to initialize NATS config", "error", err)
		os.Exit(1)
	}

	svisor := supervisor.New("flynats", 5*time.Minute)

	svisor.AddProcess(
		"exporter",
		"nats-exporter -varz 'http://fly-local-6pn:8222'",
		supervisor.WithRestart(0, 1*time.Second),
	)

	svisor.AddProcess(
		"nats-server",
		"nats-server -js -c /etc/nats.conf --logtime=false",
		supervisor.WithRestart(0, 1*time.Second),
	)

	go watchNatsConfig(natsVars)

	svisor.StopOnSignal(syscall.SIGINT, syscall.SIGTERM)

	svisor.StartHttpListener()

	err = svisor.Run()
	if err != nil {
		slog.Error("supervisor failed", "error", err)
		os.Exit(1)
	}
}

type FlyEnv struct {
	Host                string
	AppName             string
	Region              string
	GatewayRegions      []string
	ServerName          string
	Timestamp           time.Time
	StoreDir            string
	MaxFileStore        string
	MaxMemoryStore      string
	encodedAppendConfig string // base64 encoded, use AppendConfig() to get the decoded value
}

func (e FlyEnv) AppendConfig() string {
	if e.encodedAppendConfig != "" {
		b, err := base64.StdEncoding.DecodeString(e.encodedAppendConfig)
		if err != nil {
			slog.Error("error base64 decoding NATS_APPEND_CONFIG", "error", err)
			return fmt.Sprintf("// error decoding NATS_APPEND_CONFIG: %q", err.Error())
		}
		return string(b)
	}
	return ""
}

//go:embed nats.conf.tmpl
var tmplRaw string

func watchNatsConfig(vars FlyEnv) {
	slog.Info("Starting ticker")
	ticker := time.NewTicker(5 * time.Second)
	var lastReload time.Time

	go func() {
		for {
			for range ticker.C {
				newVars, err := natsConfigVars()

				if err != nil {
					slog.Error("error getting nats config vars", "error", err)
					continue
				}
				if stringSlicesEqual(vars.GatewayRegions, newVars.GatewayRegions) {
					// noop, nothing changed
					// slog.Debug("No change in regions")
					continue
				}

				cooloff := lastReload.Add(15 * time.Second)
				if time.Now().Before(cooloff) {
					slog.Info("Regions changed, but cooloff period not expired")
					continue
				}

				err = writeNatsConfig(newVars)
				if err != nil {
					slog.Error("error writing nats config", "error", err)
				}

				cmd := exec.Command(
					"nats-server",
					"--signal",
					"stop=/var/run/nats-server.pid",
				)
				slog.Info("Reloading nats",
					"old_regions", vars.GatewayRegions,
					"new_regions", newVars.GatewayRegions)
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr

				err = cmd.Run()
				if err != nil {
					slog.Error("Command finished with error", "error", err)
				}
				vars = newVars
				lastReload = time.Now()
			}
		}
	}()

	slog.Info("ticker fn return")
}

func natsConfigVars() (FlyEnv, error) {
	host := "fly-local-6pn"
	appName := os.Getenv("FLY_APP_NAME")
	storeDir := os.Getenv("NATS_STORE_DIR")
	encodedAppendConfig := os.Getenv("NATS_APPEND_CONFIG")

	var regions []string
	var err error

	if appName != "" {
		regions, err = privnet.GetRegions(context.Background(), appName)
		if err != nil {
			return FlyEnv{}, fmt.Errorf("error getting regions for app %s: %w", appName, err)
		}
	} else {
		// defaults for local exec
		host = "localhost"
		appName = "local"
		regions = []string{"local"}
	}

	if storeDir == "" {
		storeDir = "/nats-store"
	}

	// easier to compare
	sort.Strings(regions)

	region := os.Getenv("FLY_REGION")
	if region == "" {
		region = "local"
	}

	vars := FlyEnv{
		AppName:             appName,
		Region:              region,
		GatewayRegions:      regions,
		Host:                host,
		ServerName:          os.Getenv("FLY_ALLOC_ID"),
		Timestamp:           time.Now(),
		StoreDir:            storeDir,
		MaxFileStore:        os.Getenv("NATS_MAX_FILE_STORE"),
		MaxMemoryStore:      os.Getenv("NATS_MAX_MEMORY_STORE"),
		encodedAppendConfig: encodedAppendConfig,
	}
	if err != nil {
		return FlyEnv{}, err
	}
	return vars, nil
}
func initNatsConfig() (FlyEnv, error) {
	vars, err := natsConfigVars()
	if err != nil {
		return vars, err
	}
	err = writeNatsConfig(vars)

	if err != nil {
		return vars, err
	}

	return vars, nil
}

func writeNatsConfig(vars FlyEnv) error {
	tmpl, err := template.New("conf").Parse(tmplRaw)

	if err != nil {
		return err
	}

	f, err := os.Create("/etc/nats.conf")

	if err != nil {
		return err
	}

	err = tmpl.Execute(f, vars)

	if err != nil {
		return err
	}

	return nil
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}
