.PHONY: build test test-integration vet db-up db-stop run init editor-token demo
.DEFAULT_GOAL := build

init:
	@sh scripts/init-demo.sh

editor-token:
	@test -s .times-demo/editor.token || (echo 'Файл токена отсутствует. При первом запуске его создаёт сервер (make demo).' >&2; exit 1)
	@cat .times-demo/editor.token

demo: init
	$(MAKE) db-up build
	./bin/times-server --config .times-demo/server.local.yaml

build:
	go build -o bin/times ./cmd/times
	go build -o bin/times-server ./cmd/times-server

test:
	go test ./...

test-integration:
	@test -n "$(TEST_DATABASE_URL)" || (echo 'Pass TEST_DATABASE_URL=postgres://... to make test-integration'; exit 1)
	go test -race -count=1 ./internal/service -args -test-database-url '$(TEST_DATABASE_URL)'

vet:
	go vet ./...

db-up:
	docker compose up -d --wait

db-stop:
	docker compose stop

run:
	go run ./cmd/times-server
