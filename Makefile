.PHONY: up down reset-demo verify load test lint

up:
	docker compose up --build -d

down:
	docker compose down

# Veritabanını ve volume'ü siler, deterministik seed (SEED_VALUE=42) ile yeniden kurar.
reset-demo:
	docker compose down -v
	docker compose up --build -d

# F-1..F-7 ve T-1..T-4 için uçtan uca PASS/FAIL tablosu.
verify:
	docker compose --profile verify run --rm verify

# k6 yük testi: grafikleri servis içindeki /scripts/mtbf_load.js ile çalıştırır.
# Hedef API'yi değiştirmek için: K6_API_URL=http://localhost:8080 make load
load:
	docker compose --profile load run --rm k6

# 53 otomatik test (39'u gerçek PostgreSQL), race detector ile.
# DB host'a port açmadığı için testler compose ağında koşar (bkz. README §11).
test:
	docker compose --profile test run --rm test

lint:
	cd backend && go vet ./...
