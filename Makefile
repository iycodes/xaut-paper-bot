APP := xautbot
CONFIG ?= configs/config.json

.PHONY: fmt test vet build run docker-build docker-up docker-down clean package

fmt:
	gofmt -w ./cmd ./internal

test:
	go test ./...

vet:
	go vet ./...

build:
	mkdir -p bin
	go build -trimpath -o bin/$(APP) ./cmd/xautbot

run:
	go run ./cmd/xautbot -config $(CONFIG)

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
