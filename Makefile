BINARY := portfolio-editor
ADDR   ?= :8080

.PHONY: run build clean dynamo-up dynamo-down reset

run: dynamo-up build
	./$(BINARY) --addr $(ADDR)

build:
	go build -o $(BINARY) .

clean:
	rm -f $(BINARY)

dynamo-up:
	docker compose up -d dynamodb-local
	@echo "DynamoDB Local running at http://localhost:8000"

dynamo-down:
	docker compose down

reset:
	docker compose down -v
	@echo "DynamoDB data wiped"
