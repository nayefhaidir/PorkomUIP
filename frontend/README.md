# UIP Meeting Portal Frontend

The Next.js frontend is independently runnable and communicates with the Go API
through same-origin `/api` routes.

```sh
npm ci
GO_API_URL=http://localhost:8080 npm run dev
```

Set `GO_API_URL` to the backend origin for development when it is not listening
on `http://localhost:8080`. For standalone production builds, provide it at
build time; the Compose configuration does this automatically. `GET /healthz`
checks the frontend process without depending on the API. Backend health is
checked separately at `/api/health`.
