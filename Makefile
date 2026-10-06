# DSN of the database started by docker-compose.yml; override it to test
# against another database.
BOA_TEST_DSN ?= postgres://postgres:postgres@localhost:5442/boa?sslmode=disable
export BOA_TEST_DSN

.PHONY: test db-up db-down

# Starts the test database, vets and runs every test (integration tests
# included), then removes the database whatever the outcome.
test: db-up
	@go vet ./... && go test -count=1 ./...; status=$$?; \
		docker compose down -v; exit $$status

db-up:
	docker compose up -d --wait

db-down:
	docker compose down -v
