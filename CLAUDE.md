# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This repository contains code for creating a federated mesh of NATS servers that communicate over the private, encrypted IPv6 network available to Fly organizations. NATS is an open source messaging backend used for internal communications.

The application sets up NATS servers that can discover and connect to each other across regions using Fly's internal networking capabilities.

## Architecture

1. **Supervisor System**:
   - The `supervisor` package implements a process supervisor that manages the NATS server and related processes
   - Handles process lifecycle, restarts, and signal handling
   - Provides output multiplexing for logging

2. **Private Network**:
   - The `privnet` package provides utilities for working with Fly's private IPv6 network
   - Enables discovery of other instances across regions
   - Uses DNS to locate peers and retrieve region information

3. **Health Checks**:
   - The `check` and `flycheck` packages provide health check functionality
   - Used to verify the NATS server is functioning correctly
   - Exposes HTTP endpoints for monitoring

4. **Main Application**:
   - Lives in `cmd/start/main.go`
   - Configures and starts NATS server using environment variables
   - Watches for region changes and updates NATS configuration
   - Uses a template system for NATS configuration

## Build & Development Commands

```bash
# Build the application
go build -o start ./cmd/start

# Build the Docker image
docker build -t jeffh/nats-cluster .

# Run tests
go test ./...

# Run with local development setup
go run ./cmd/start/main.go
```

## Deployment

The application is designed to be deployed on Fly.io. Key steps:

1. `fly launch --no-deploy`
2. Configure `fly.toml` with appropriate settings
3. `fly deploy`
4. Scale up: `fly scale count 3`
5. Add more regions: `fly scale count 3 --region sjc`

## Environment Variables

- `NATS_STORE_DIR`: Directory where NATS will store data (default: `/nats-store`)
- `NATS_MAX_FILE_STORE`: Maximum size of file storage for jetstream (default: `1TB`)
- `NATS_MAX_MEM_STORE`: Maximum size of memory storage for jetstream (default: 75% of memory)
- `NATS_APPEND_CONFIG`: Base64 encoded string to append to NATS configuration file
