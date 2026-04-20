BINARY := portfolio-editor
ADDR   ?= :8080
STORAGE_BACKEND ?= $(shell grep '^STORAGE_BACKEND=' .env 2>/dev/null | cut -d= -f2)

.PHONY: run build clean dynamo-up dynamo-down reset

run: build
ifeq ($(STORAGE_BACKEND),gist)
	./$(BINARY) --addr $(ADDR)
else
	$(MAKE) dynamo-up
	./$(BINARY) --addr $(ADDR)
endif

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
