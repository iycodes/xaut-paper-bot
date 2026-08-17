APP := xautbot
DIAG := xautdiag
CONFIG ?= configs/config.json
DIAG_OUTPUT ?= xaut-diagnostic.json

.PHONY: fmt test vet build run diagnose docker-build docker-up docker-down clean package

fmt:
	gofmt -w ./cmd ./internal

test:
	go test ./...

vet:
	go vet ./...

build:
	mkdir -p bin
	go build -trimpath -o bin/$(APP) ./cmd/xautbot
	go build -trimpath -o bin/$(DIAG) ./cmd/xautdiag

run:
	go run ./cmd/xautbot -config $(CONFIG)

diagnose:
	mkdir -p bin
	go build -trimpath -o bin/$(DIAG) ./cmd/xautdiag
	./bin/$(DIAG) -config $(CONFIG) -output $(DIAG_OUTPUT)

docker-build:
	docker compose build

docker-up:
	docker compose up -d --build

docker-down:
	docker compose down

clean:
	rm -rf bin dist

package:
	mkdir -p dist
	zip -r dist/xaut-paper-bot.zip . \
		-x '.git/*' '.env' 'data/*' 'bin/*' 'dist/*' '*.zip'
