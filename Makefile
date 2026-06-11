.PHONY: build test vet test-integration

build:
	go build ./...

vet:
	go vet ./...

test:
	go test -tags testing ./...

# test-integration brings up the test database and runs integration tests.
# Teardown always runs even on test failure to avoid resource leaks and orphaned containers.
# -p 1 serializes package test binaries: they share one database, and concurrent
# TRUNCATEs across packages would destroy each other's fixtures mid-run.
test-integration:
	docker compose -f docker-compose.test.yml up -d --wait && \
	VALORY_TEST_DATABASE_URL=postgres://valory_test:valory_test@localhost:55432/valory_test?sslmode=disable go test -tags integration -p 1 ./... ; \
	status=$$?; \
	docker compose -f docker-compose.test.yml down -v; \
	exit $$status
