BIN  := bin/big2-tui
PORT ?= 2222

.PHONY: build run test vet preview tidy clean smoke

build:
	go build -o $(BIN) ./cmd/server

run: build
	./$(BIN) -port $(PORT)

test:
	go test ./... -race

vet:
	go vet ./...

preview:
	go run ./cmd/preview

tidy:
	go mod tidy

clean:
	rm -rf bin big2-tui.log

# Print how to drive a local multi-player smoke test.
smoke: build
	@echo "1) In this terminal:   ./$(BIN) -port $(PORT)"
	@echo "2) In 2-3 others:      ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -p $(PORT) localhost"
	@echo "3) Host presses enter to start (2+ players); + adds a bot."
