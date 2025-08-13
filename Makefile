.PHONY: build test dev

# Locally build the Docker image for the stream-webpage container
build:
	@echo "Building the stream-webpage-container Docker image locally..."
	docker compose build stream-webpage

# Run the application (and no test RTMP server) in a Docker container
run:
	@echo "Starting the stream-webpage container..."
	docker compose up --build stream-webpage

# Run the tests in the container, generate coverage reports, and save them to the coverage directory locally
test:
	@echo "Running tests and generating coverage report..."
	@mkdir -p coverage
	docker compose run --rm --build -v $(PWD)/coverage:/app/coverage test go test -coverprofile=coverage/coverage.out ./...
	docker compose run --rm -v $(PWD)/coverage:/app/coverage test go tool cover -html=coverage/coverage.out -o coverage/coverage.html
	@echo "Coverage report generated in coverage/coverage.html"

# Run both the application and the RTMP test server
dev:
	@echo "Starting development environment..."
	docker compose up --build stream-webpage rtmp-server