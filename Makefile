BINARY := portfolio-editor
CONFIG ?= config.json
ADDR   ?= :8080

.PHONY: run build clean

run: build
	./$(BINARY) --config $(CONFIG) --addr $(ADDR)

build:
	go build -o $(BINARY) .

clean:
	rm -f $(BINARY)
