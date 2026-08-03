.PHONY: tidy build test run-local dashboard docker-up docker-down bench

SERVICES = gateway router scheduler worker registry chaos

tidy:
	go mod tidy

build:
	@for s in $(SERVICES); do \
		echo "building $$s"; \
		go build -o bin/$$s ./services/$$s/cmd; \
	done

test:
	go test ./...

run-local: build
	@bash scripts/run-local.sh

dashboard:
	cd dashboard && npm install && npm run dev

docker-up:
	docker compose up --build -d

docker-down:
	docker compose down -v

bench:
	curl -s -X POST http://localhost:8090/v1/benchmark \
	  -H 'Content-Type: application/json' \
	  -d '{"concurrency":4,"max_tokens":64}' | python -m json.tool

infer:
	curl -s http://localhost:8080/v1/chat/completions \
	  -H 'Content-Type: application/json' \
	  -H 'X-API-Key: lattice-dev-key' \
	  -d '{"messages":[{"role":"user","content":"Write a Go function to merge two sorted slices."}],"max_tokens":64,"policy":"balanced"}' | python -m json.tool
