.PHONY: up down reset-demo verify load test

up:
	docker compose up --build -d

down:
	docker compose down

reset-demo:
	docker compose down -v
	docker compose up --build -d

verify:
	docker compose --profile verify run --rm verify

load:
	docker compose --profile load run --rm k6 run /scripts/mtbf_load.js

test:
	cd backend && go test -v -race ./...