BINARY := signet
BIN_DIR := bin

.PHONY: build install test vet acceptance examples examples-report clean

build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY) ./cmd/signet

install:
	go install ./cmd/signet

test:
	go test ./...

vet:
	go vet ./...

acceptance: build
	./$(BIN_DIR)/$(BINARY) run acceptance.yaml --yes

# The examples module tests its own kv binary with the Go SDK.
examples:
	go -C examples vet ./...
	go -C examples test -count=1 ./...

# Run the examples and print their feature document.
examples-report: build
	go -C examples test -json -count=1 ./... | ./$(BIN_DIR)/$(BINARY) report

clean:
	rm -rf $(BIN_DIR) $(BINARY) examples/bin
