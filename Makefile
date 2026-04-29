.PHONY: build test test-fly

build:
	go build -o start ./cmd/start

test:
	go test ./...

# Run the end-to-end Fly.io test harness. Builds and deploys an ephemeral
# fly app, runs assertions, then destroys it.
#
# Pass extra flags via FLAGS:
#   make test-fly FLAGS="-regions=iad,sjc -count=2"
#   make test-fly FLAGS="-keep"  # leave app alive on exit for debugging
test-fly:
	cd test/e2e && go run . $(FLAGS)
