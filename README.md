# lattice

## run locally

```bash
make build
make run-local
```

in another terminal:

```bash
cd dashboard
npm install
npm run dev
```

api: http://127.0.0.1:8080  
dashboard: http://127.0.0.1:3000  
api key: `lattice-dev-key`

smoke test:

```bash
make infer
```

## run with docker

```bash
docker compose up --build
```

api: http://localhost:8080  
dashboard: http://localhost:3000  
grafana: http://localhost:3001  
prometheus: http://localhost:9090
