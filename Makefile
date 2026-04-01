BINARY := portfolio-editor
ADDR   ?= :8080

.PHONY: run build clean

run: build
	./$(BINARY) --addr $(ADDR)

build:
	go build -o $(BINARY) .

clean:
	rm -f $(BINARY)
