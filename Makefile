BINARY := signet
BIN_DIR := bin

.PHONY: build install test vet acceptance clean

build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY) .

install:
	go install .

test:
	go test ./...

vet:
	go vet ./...

acceptance: build
	./$(BIN_DIR)/$(BINARY) run acceptance.yaml --yes

clean:
	rm -rf $(BIN_DIR)
